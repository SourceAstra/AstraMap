/**
 * SourceAstra - Understand Module v2.0 (完全重写)
 * Design.md 3.0 规格：追踪视图 (核心攻坚区)
 * 
 * 架构：双面板布局
 *   左: D3 Canvas 追踪拓扑图 (DAG flow + 流光动画)
 *   右: C 源码预览 (语法高亮)
 * 
 * 核心能力：
 *   - 多终点路径裁剪 (Path Clipping)
 *   - 流光影响分析 (Impact Flow Animation)
 *   - 代码一体化协同
 */
(function() {
    let initialized = false;
    let traceCanvas, traceCtx;
    let traceSim = null;
    let traceZoom = null;
    let traceXform = { k: 1, x: 0, y: 0 };
    let traceNodes = [];
    let traceLinks = [];
    let traceNodeMap = new Map(); // O(1) 节点索引
    let _graphDirty = true; // 脏标记：仅数据/变换变更时全量重绘
    let _cachedNodeGrads = {}; // 缓存节点渐变，按类型复用
    let rawNodes = [];
    let rawLinks = [];
    let _cachedComplexity = {};
    let showBlastRadius = false;
    let _blastRadiusCache = {};

    // 页面级工具函数
    function hexToRgbA(hex, alpha) {
        let c = hex.substring(1);
        if (c.length === 3) c = c[0] + c[0] + c[1] + c[1] + c[2] + c[2];
        const r = parseInt(c.substring(0, 2), 16);
        const g = parseInt(c.substring(2, 4), 16);
        const b = parseInt(c.substring(4, 6), 16);
        return `rgba(${r}, ${g}, ${b}, ${alpha})`;
    }

    function directoryGroupForNode(node) {
        if (!node || !node.file) {
            return node && node.type === 'unknown'
                ? { id: 'external', name: '外部依赖', level: 6 }
                : { id: '(root)', name: '(root)', level: 3 };
        }
        const parts = node.file.replace(/\\/g, '/').split('/');
        const dir = parts.length > 1 && parts[0] ? parts[0] : '(root)';
        return { id: dir, name: dir, level: 3 };
    }

    let showSystem = false;
    let useGrouping = true; // 默认开启目录分组与层级布局
    let selectedModuleFilter = 'all'; // 选中的目录过滤器，'all'表示不进行目录级过滤
    let modulesList = []; // 保留兼容变量，独立 AstraMap 不依赖模块注册表
    let rootNodeId = null;
    let selectedNodeId = null;
    let flowAnimFrame = null;
    let flowOffset = 0;

    const COLORS = {
        root: '#f59e0b',
        function: '#38bdf8',
        unknown: '#64748b',
        edge: 'rgba(99, 102, 241, 0.6)',
        impact: '#ef4444',
        highlight: '#818cf8'
    };

    const Understand = {
        isTraceableSymbol(sym) {
            return !!sym && (sym.type === 'function' || sym.kind === 'function' || sym.type === 'method' || sym.kind === 'method');
        },

        init(nodeOrId) {
            const isInitialized = window.SourceAstra && window.SourceAstra.traceInitialized;
            if (nodeOrId) {
                const sym = (typeof nodeOrId === 'string') ? (window.G_DATA ? window.G_DATA.symbols[nodeOrId] : null) : nodeOrId;
                if (this.isTraceableSymbol(sym)) {
                    window.G_SELECTED = sym;
                    if (isInitialized) { this.startTrace(sym); return; }
                }
            } else if (isInitialized) {
                if (this.isTraceableSymbol(window.G_SELECTED)) {
                    this.startTrace(window.G_SELECTED);
                }
                this.renderFunctionTree(document.getElementById('und-function-search')?.value || '');
                const layout = document.querySelector('.und-layout');
                if (layout) layout.style.display = 'flex';
                return;
            }
            if (window.SourceAstra) {
                window.SourceAstra.traceInitialized = true;
            }
            console.log('[Understand v2] Initializing...');
            const container = document.getElementById('view-trace');
            if (!container) return;

            container.innerHTML = `
                <div class="und-layout">
                    <div class="und-tree-panel">
                        <div class="und-tree-header">
                            <div class="und-tree-title">相关函数</div>
                            <input id="und-function-search" class="und-function-search" placeholder="搜索函数..." autocomplete="off">
                        </div>
                        <div id="und-function-tree" class="und-function-tree"></div>
                    </div>
                    <div class="und-graph-panel">
                        <div class="und-graph-toolbar">
                            <div class="und-toolbar-left">
                                <span class="und-toolbar-title">🧠 相关调用链</span>
                                <span class="und-root-label" id="und-root-label"></span>
                            </div>
                            <div class="und-toolbar-right">
                                <label class="und-depth-label">深度
                                    <select id="und-depth-select" class="und-select">
                                        <option value="1">1</option>
                                        <option value="2">2</option>
                                        <option value="3">3</option>
                                        <option value="4">4</option>
                                        <option value="5">5</option>
                                        <option value="6">6</option>
                                        <option value="8" selected>8</option>
                                    </select>
                                </label>
                                <label class="und-depth-label" style="margin-right:8px;">透视目录
                                    <select id="und-module-select" class="und-select" style="min-width:110px;">
                                        <option value="all" selected>全部目录</option>
                                    </select>
                                </label>
                                <button id="und-btn-group" class="und-btn" title="切换目录与层级分组布局">📂 目录分组</button>
                                <button id="und-btn-impact" class="und-btn" title="查看与当前函数相关的调用影响">⚡ 相关影响</button>
                                <button id="und-btn-blast" class="und-btn" title="查看与当前函数相关的覆盖范围">💣 相关范围</button>
                                <button id="und-btn-fit" class="und-btn" title="适应画布">⊞ 适应</button>
                                <button id="und-btn-detail" class="und-btn" title="显示更完整的相关调用关系">📋 详细关系</button>
                            </div>
                        </div>
                        <canvas id="und-canvas"></canvas>
                        <div class="und-legend">
                            <div class="und-legend-item"><span class="und-dot" style="background:#f59e0b"></span>起点</div>
                            <div class="und-legend-item"><span class="und-dot" style="background:#38bdf8"></span>函数</div>
                            <div class="und-legend-item"><span class="und-dot" style="background:#64748b"></span>外部</div>
                            <div class="und-legend-item"><span class="und-dot" style="background:#ef4444"></span>影响路径</div>
                        </div>
                        <div class="und-code-toggle" id="und-code-toggle" title="收起源码预览">›</div>
                    </div>
                    <div class="und-resizer" id="und-resizer"></div>
                    <div class="und-code-panel">
                        <div class="und-code-header">
                            <span id="und-code-title">📄 源码预览</span>
                            <span id="und-code-file" class="und-code-file"></span>
                        </div>
                        <div id="und-quality-panel" class="und-quality-panel" style="display:none;"></div>
                        <div class="und-code-body" id="und-code-body">
                            <div class="und-code-empty">
                                <div style="font-size:32px">📋</div>
                                <div>点击拓扑图中的节点查看源码</div>
                            </div>
                        </div>
                    </div>
                </div>
                <div class="und-empty-state" id="und-empty">
                    <div style="font-size:48px">🧠</div>
                    <h3>相关调用视图</h3>
                    <p>从左侧函数树选择入口，查看与当前函数相关的祖先链和子孙链</p>
                </div>
            `;

            this.bindEvents();
            this.hookGlobalSelection();
            this.initResizer();
            this.renderFunctionTree();
            document.querySelector('.und-layout').style.display = 'flex';

            if (this.isTraceableSymbol(window.G_SELECTED)) {
                this.startTrace(window.G_SELECTED);
            }
        },

        bindEvents() {
            const functionSearch = document.getElementById('und-function-search');
            if (functionSearch) {
                functionSearch.oninput = () => this.renderFunctionTree(functionSearch.value);
            }

            const depthSelect = document.getElementById('und-depth-select');
            if (depthSelect) {
                depthSelect.onchange = () => {
                    if (rootNodeId) this.fetchTrace(rootNodeId, parseInt(depthSelect.value));
                };
            }

            const moduleSelect = document.getElementById('und-module-select');
            if (moduleSelect) {
                moduleSelect.onchange = (e) => {
                    selectedModuleFilter = e.target.value;
                    this.saveCurrentPositions();
                    this.applyFilters();
                    this.buildGraph();
                };
            }

            const btnImpact = document.getElementById('und-btn-impact');
            if (btnImpact) {
                btnImpact.onclick = () => this.toggleImpactFlow();
            }

            const btnBlast = document.getElementById('und-btn-blast');
            if (btnBlast) {
                btnBlast.onclick = () => this.toggleBlastRadius();
            }

            const btnFit = document.getElementById('und-btn-fit');
            if (btnFit) {
                btnFit.onclick = () => this.fitToCanvas();
            }

            const btnDetail = document.getElementById('und-btn-detail');
            if (btnDetail) {
                btnDetail.onclick = () => this.toggleSystemFunctions();
                // 初始状态同步按钮样式
                if (showSystem) btnDetail.classList.add('active');
                else btnDetail.classList.remove('active');
            }

            const btnGroup = document.getElementById('und-btn-group');
            if (btnGroup) {
                btnGroup.onclick = () => this.toggleGrouping();
                if (useGrouping) btnGroup.classList.add('active');
                else btnGroup.classList.remove('active');
            }
        },

        hookGlobalSelection() {
            const tryHook = () => {
                const originalFocusNode = window.focusNode;
                if (typeof originalFocusNode === 'function') {
                    window.focusNode = (nodeOrId) => {
                        originalFocusNode(nodeOrId);
                        const G_DATA = window.G_DATA;
                        const sym = (typeof nodeOrId === 'string') ? (G_DATA ? G_DATA.symbols[nodeOrId] : null) : nodeOrId;
                        if (this.isTraceableSymbol(sym)) {
                            this.startTrace(sym);
                        }
                    };
                } else {
                    setTimeout(tryHook, 100);
                }
            };
            tryHook();
        },

        renderFunctionTree(filterText = '') {
            const tree = document.getElementById('und-function-tree');
            if (!tree) return;

            const symbols = window.G_DATA && window.G_DATA.symbols ? Object.values(window.G_DATA.symbols) : [];
            const needle = (filterText || '').trim().toLowerCase();
            const funcs = symbols
                .filter(s => s && (s.type === 'function' || s.kind === 'function' || s.kind === 'method'))
                .filter(s => {
                    if (!needle) return true;
                    const hay = `${s.name || ''} ${s.qualifiedName || ''} ${s.file || s.filePath || ''}`.toLowerCase();
                    return hay.includes(needle);
                })
                .sort((a, b) => {
                    const af = a.file || a.filePath || '';
                    const bf = b.file || b.filePath || '';
                    if (af !== bf) return af.localeCompare(bf);
                    return (a.startLine || a.line || 0) - (b.startLine || b.line || 0);
                })
                .slice(0, needle ? 500 : 1200);

            if (!funcs.length) {
                tree.innerHTML = '<div class="und-tree-empty">没有匹配的函数</div>';
                return;
            }

            const groups = new Map();
            funcs.forEach(fn => {
                const file = fn.file || fn.filePath || '(unknown file)';
                const parts = file.replace(/\\/g, '/').split('/');
                const dir = parts.length > 1 && parts[0] ? parts[0] : '(root)';
                if (!groups.has(dir)) groups.set(dir, new Map());
                const fileMap = groups.get(dir);
                if (!fileMap.has(file)) fileMap.set(file, []);
                fileMap.get(file).push(fn);
            });

            const html = Array.from(groups.entries()).map(([dir, files]) => {
                const fileHtml = Array.from(files.entries()).map(([file, fileFuncs]) => {
                    const fnHtml = fileFuncs.map(fn => {
                        const active = fn.id === rootNodeId ? ' active' : '';
                        const line = fn.startLine || fn.line || 0;
                        const idx = funcs.indexOf(fn);
                        return `<button class="und-tree-function${active}" data-node-index="${idx}" title="${this.escapeHtml(fn.qualifiedName || fn.name || fn.id)}">
                            <span class="und-tree-function-name">${this.escapeHtml(fn.name || fn.id)}</span>
                            <span class="und-tree-function-line">${line ? ':' + line : ''}</span>
                        </button>`;
                    }).join('');
                    return `<details class="und-tree-file" ${needle ? 'open' : ''}>
                        <summary>${this.escapeHtml(file.split('/').pop() || file)} <span>${fileFuncs.length}</span></summary>
                        ${fnHtml}
                    </details>`;
                }).join('');
                return `<details class="und-tree-dir" open>
                    <summary>${this.escapeHtml(dir)} <span>${Array.from(files.values()).reduce((n, arr) => n + arr.length, 0)}</span></summary>
                    ${fileHtml}
                </details>`;
            }).join('');

            tree.innerHTML = html;
            tree.querySelectorAll('.und-tree-function').forEach(btn => {
                btn.onclick = () => {
                    const idx = Number(btn.getAttribute('data-node-index'));
                    const sym = Number.isInteger(idx) ? funcs[idx] : null;
                    if (sym) this.startTrace(sym);
                };
            });
        },

        startTrace(sym) {
            selectedModuleFilter = 'all';
            document.getElementById('und-empty').style.display = 'none';
            document.querySelector('.und-layout').style.display = 'flex';
            
            rootNodeId = sym.id || sym.name;
            const depthSelect = document.getElementById('und-depth-select');
            const depth = depthSelect ? parseInt(depthSelect.value) : 1;
            
            const fileLabel = sym.file || sym.filePath || '';
            const lineLabel = sym.startLine || sym.line || '';
            document.getElementById('und-root-label').textContent = [sym.name, fileLabel ? `${fileLabel}${lineLabel ? ':' + lineLabel : ''}` : ''].filter(Boolean).join(' · ');
            this.renderFunctionTree(document.getElementById('und-function-search')?.value || '');

            selectedNodeId = rootNodeId;
            this.showNodeInfo(sym);
            this.loadCodePreview(sym);
            
            this.initCanvas();
            this.fetchTrace(rootNodeId, depth);
        },

        initCanvas() {
            traceCanvas = document.getElementById('und-canvas');
            if (!traceCanvas) return;

            // Wait for layout to settle
            requestAnimationFrame(() => {
                this.resizeTraceCanvas();
            });
            
            // Also set initial size synchronously as fallback
            this.resizeTraceCanvas();
            traceCtx = traceCanvas.getContext('2d');

            // Zoom
            if (!traceZoom) {
                traceZoom = d3.zoom().scaleExtent([0.1, 5]).on('zoom', (e) => {
                    traceXform = e.transform;
                    this.renderGraph();
                });
                d3.select(traceCanvas).call(traceZoom);
                
                 // Click handler
                 d3.select(traceCanvas).on('click', (event) => {
                     const [mx, my] = d3.pointer(event, traceCanvas);
                     const sx = (mx - traceXform.x) / traceXform.k;
                     const sy = (my - traceXform.y) / traceXform.k;
 
                     let closest = null;
                     traceNodes.forEach(n => {
                         if (n.x == null) return;
                         const nw = n._nw || 220;
                         const nh = 50;
                         const rx = n.x - nw / 2;
                         const ry = n.y - nh / 2;
                         if (sx >= rx && sx <= rx + nw && sy >= ry && sy <= ry + nh) {
                             closest = n;
                         }
                     });
 
                     if (closest) {
                         selectedNodeId = closest.id;
                         this.showNodeInfo(closest);
                         this.loadCodePreview(closest);
                         this.renderGraph();
                         return;
                     }

                     // Check if click is on a module box header (title bar)
                     if (useGrouping && this.renderedModuleBoxes) {
                         let clickedBox = null;
                         this.renderedModuleBoxes.forEach(b => {
                             const rx = b.absX;
                             const ry = b.absY;
                             const rw = b.boxW;
                             if (sx >= rx && sx <= rx + rw && sy >= ry && sy <= ry + 28) {
                                 clickedBox = b;
                             }
                         });
                         if (clickedBox) {
                             selectedModuleFilter = clickedBox.id;
                             this.saveCurrentPositions();
                             this.applyFilters();
                             this.buildGraph();
                             return;
                         }
                     }
                 });

                // Right Click Context Menu Handler
                traceCanvas.addEventListener('contextmenu', (event) => {
                    if (!traceNodes.length) return;
                    const [mx, my] = d3.pointer(event, traceCanvas);
                    const sx = (mx - traceXform.x) / traceXform.k;
                    const sy = (my - traceXform.y) / traceXform.k;

                    let closest = null;
                    traceNodes.forEach(n => {
                        if (n.x == null) return;
                        const nw = n._nw || 220;
                        const nh = 50;
                        const rx = n.x - nw / 2;
                        const ry = n.y - nh / 2;
                        if (sx >= rx && sx <= rx + nw && sy >= ry && sy <= ry + nh) {
                            closest = n;
                        }
                    });

                    if (closest) {
                        event.preventDefault();
                        event.stopPropagation();
                        const sym = (window.G_DATA && window.G_DATA.symbols[closest.id]) || closest;
                        if (window.SourceAstra && window.SourceAstra.showContextMenu) {
                            window.SourceAstra.showContextMenu(event.clientX, event.clientY, sym.id || closest.id, sym.name || closest.name);
                        }
                    }
                });
            }
        },

        async fetchTrace(nodeId, depth) {
            const jobId = sessionStorage.getItem('sourceastra_job_id');
            const url = `/api/trace?node_id=${encodeURIComponent(nodeId)}&depth=${depth}${jobId ? '&job=' + jobId : ''}`;

            try {
                const [modulesRes, traceRes] = await Promise.all([
                    fetch('/api/modules'),
                    fetch(url)
                ]);
                const modulesData = await modulesRes.json();
                const data = await traceRes.json();
                if (data.error) throw new Error(data.error);

                modulesList = modulesData || [];

                rawNodes = (data.nodes || []).map(n => ({ ...n }));
                rawLinks = (data.links || []).map(l => ({
                    source: l.from,
                    target: l.to
                }));

                this.applyFilters();
                this.buildGraph();

                // 提取唯一文件，并发加速拉取文件复杂度
                const files = new Set();
                rawNodes.forEach(n => {
                    if (n.file) files.add(n.file);
                });
                [...files].forEach(async (f) => {
                    try {
                        const compRes = await fetch('/api/complexity/calculate', {
                            method: 'POST',
                            headers: { 'Content-Type': 'application/json' },
                            body: JSON.stringify({ file_path: f })
                        });
                        if (compRes.ok) {
                            const results = await compRes.json();
                            if (Array.isArray(results)) {
                                results.forEach(metrics => {
                                    if (metrics.symbol_id) {
                                        _cachedComplexity[metrics.symbol_id] = metrics;
                                    }
                                });
                                this.renderGraph();
                            }
                        }
                    } catch (err) {
                        console.warn('[Understand v2] Failed to pre-fetch complexity for', f, err);
                    }
                });
            } catch (e) {
                console.error('[Understand v2] Trace error:', e);
            }
        },

        buildGraph() {
            if (traceSim) { traceSim.stop(); traceSim = null; }

            const canvas = document.getElementById('und-canvas');
            if (!canvas || !traceNodes.length) return;

            // 重建 O(1) 节点索引
            traceNodeMap = new Map(traceNodes.map(n => [n.id, n]));

            window.SourceAstra.traceNodes = traceNodes;
            window.SourceAstra.traceLinks = traceLinks;
            window.SourceAstra.traceRootId = rootNodeId;

            // Pre-calculate node widths based on font measurement
            const tempCtx = canvas.getContext('2d');
            traceNodes.forEach(n => {
                const isUnknown = n.type === 'unknown';
                tempCtx.save();
                tempCtx.font = 'bold 12px "Inter", "Outfit", sans-serif';
                const nameWidth = tempCtx.measureText(n.name).width;
                const fileShort = n.file ? n.file.split('/').pop() : (isUnknown ? '外部依赖' : '系统模块');
                tempCtx.font = '500 9px "JetBrains Mono", monospace';
                const fileWidth = tempCtx.measureText(fileShort).width + (n.line ? 40 : 0);
                tempCtx.restore();
                n._nw = Math.max(220, Math.max(nameWidth, fileWidth) + 36);
            });

            // 保持由 applyFilters 预先计算的完整图 _layer 拓扑深度属性
            traceNodes.forEach(n => {
                if (n._layer === undefined) {
                    n._layer = 0;
                }
                n.fx = null;
                n.fy = null;
            });

            if (useGrouping) {
                // ===== 目录与层级分组布局定位算法 =====
                traceNodes.forEach(n => {
                    const group = directoryGroupForNode(n);
                    n._groupModuleId = group.id;
                    n._groupModuleName = group.name;
                    n._groupLevel = group.level;
                    n.fx = null;
                    n.fy = null;
                });

                // 对于 virtual_aggregate，其 module 继承自其 parentId 节点
                traceNodes.forEach(n => {
                    if (n.type === 'virtual_aggregate' && n.parentId) {
                        const parent = traceNodes.find(p => p.id === n.parentId);
                        if (parent) {
                            n._groupModuleId = parent._groupModuleId;
                            n._groupModuleName = parent._groupModuleName;
                            n._groupLevel = parent._groupLevel;
                        }
                    }
                });

                // 按照 level 和 module 聚类
                const levelGroups = {};
                traceNodes.forEach(n => {
                    const l = n._groupLevel;
                    const m = n._groupModuleId;
                    if (!levelGroups[l]) levelGroups[l] = {};
                    if (!levelGroups[l][m]) levelGroups[l][m] = [];
                    levelGroups[l][m].push(n);
                });

                // 计算当前视图拥有的目录总数
                let totalModuleCount = 0;
                Object.keys(levelGroups).forEach(l => {
                    totalModuleCount += Object.keys(levelGroups[l]).length;
                });

                const levelList = Object.keys(levelGroups).map(Number).sort((a, b) => a - b);
                const moduleBoxes = [];
                const levelHeight = {};

                levelList.forEach(l => {
                    const mods = levelGroups[l];
                    const modIds = Object.keys(mods).sort();
                    
                    modIds.forEach(mId => {
                        const nodes = mods[mId];
                        
                        // 根据当前视图拥有的目录数量动态决定目标宽度和最大列数以利用大空间
                        const availableWidth = traceCanvas ? Math.max(800, traceCanvas.width - 160) : 1200;
                        const maxCols = totalModuleCount === 1 ? 8 : 4;
                        const targetWidth = totalModuleCount === 1 ? availableWidth : 820;
                        
                        // 1. 按照 _layer 属性（拓扑深度）对节点进行分组以体现真实的层级关系
                        const layerMap = {};
                        nodes.forEach(n => {
                            const ly = n._layer != null ? n._layer : 0;
                            if (!layerMap[ly]) layerMap[ly] = [];
                            layerMap[ly].push(n);
                        });

                        const sortedLayers = Object.keys(layerMap).map(Number).sort((a, b) => a - b);
                        
                        // 计算每个层级格栅的物理宽度，并求得最大宽度 innerW 以确保整个目录框整齐划一
                        const layerLayouts = [];
                        let maxLayerW = 220;

                        sortedLayers.forEach(ly => {
                            const rowNodes = layerMap[ly];
                            rowNodes.sort((a, b) => a.name.localeCompare(b.name));
                            
                            const count = rowNodes.length;
                            // 动态计算该层级内卡片的平均宽度
                            let sumW = 0;
                            rowNodes.forEach(n => sumW += n._nw);
                            const avgW = sumW / count;

                            // 根据卡片平均宽度动态计算列数
                            const cols = Math.max(1, Math.min(count, Math.min(maxCols, Math.floor(targetWidth / (avgW + 40)))));
                            
                            // 初始化每列的最大卡片宽度数组
                            const colMaxW = Array(cols).fill(0);
                            rowNodes.forEach((node, idx) => {
                                const col = idx % cols;
                                if (node._nw > colMaxW[col]) {
                                    colMaxW[col] = node._nw;
                                }
                            });

                            // 计算每一列的中心点 X 坐标
                            const colCenters = [];
                            let currentX = 0;
                            for (let c = 0; c < cols; c++) {
                                colCenters[c] = currentX + colMaxW[c] / 2;
                                currentX += colMaxW[c] + 40; // 40px 列间距
                            }

                            const layerW = currentX - 40;
                            if (layerW > maxLayerW) maxLayerW = layerW;

                            layerLayouts.push({
                                ly: ly,
                                nodes: rowNodes,
                                cols: cols,
                                colCenters: colCenters,
                                layerW: layerW
                            });
                        });

                        const innerW = maxLayerW;
                        let currentY = 0;
                        const cardH = 50;
                        const rowGapY = 16;    // 层内行与行之间的垂直间距
                        const layerGapY = 40;  // 层与层之间的垂直间距
                        const dividers = [];

                        // 2. 依次纵向排布每个层级。层级内节点根据自适应列数折行，均衡排布。
                        layerLayouts.forEach((layout, rowIdx) => {
                            const { nodes: rowNodes, cols, colCenters, layerW } = layout;
                            const layerOffset = (innerW - layerW) / 2; // 用于在目录内水平居中该层级
                            
                            const startY = currentY;
                            let maxRowInLayer = 0;

                            rowNodes.forEach((node, idx) => {
                                const col = idx % cols;
                                const row = Math.floor(idx / cols);
                                if (row > maxRowInLayer) maxRowInLayer = row;

                                node.relX = layerOffset + colCenters[col];
                                node.relY = startY + row * (cardH + rowGapY);
                            });

                            const layerRowsCount = maxRowInLayer + 1;
                            const layerHeight = layerRowsCount * cardH + (layerRowsCount - 1) * rowGapY;
                            currentY = startY + layerHeight;

                            // 如果不是最后一层，在当前层 and 下一层之间记录分割线位置
                            if (rowIdx < layerLayouts.length - 1) {
                                dividers.push(currentY + layerGapY / 2);
                                currentY += layerGapY;
                            }
                        });

                        const innerH = currentY;
                        const paddingX = 32;
                        const paddingY = 32;
                        const boxW = innerW + paddingX * 2;
                        const boxH = innerH + paddingY * 2 + 35; // 35px 标题栏高度

                        moduleBoxes.push({
                            level: l,
                            moduleId: mId,
                            moduleName: nodes[0]._groupModuleName,
                            boxW: boxW,
                            boxH: boxH,
                            nodes: nodes,
                            paddingX: paddingX,
                            paddingY: paddingY,
                            dividers: dividers  // 传递分割线相对 Y 坐标数组给渲染层
                        });
                    });
                });

                // 计算水平排布
                levelList.forEach(l => {
                    const levelBoxes = moduleBoxes.filter(b => b.level === l);
                    const gapX = 60;
                    const totalW = levelBoxes.reduce((sum, b) => sum + b.boxW, 0) + (levelBoxes.length - 1) * gapX;
                    
                    let startX = -totalW / 2;
                    let maxH = 0;
                    levelBoxes.forEach(b => {
                        b.boxX = startX;
                        startX += b.boxW + gapX;
                        b.boxY = 0;
                        if (b.boxH > maxH) maxH = b.boxH;
                    });
                    levelHeight[l] = maxH;
                });

                // 计算垂直排布
                const gapY = 140;
                let currentY = 100;
                levelList.forEach(l => {
                    const levelBoxes = moduleBoxes.filter(b => b.level === l);
                    levelBoxes.forEach(b => {
                        b.boxY = currentY;
                    });
                    currentY += levelHeight[l] + gapY;
                });

                const canvasW = canvas.width || 1000;
                const centerX = canvasW / 2;
                
                moduleBoxes.forEach(b => {
                    b.absX = centerX + b.boxX;
                    b.absY = b.boxY;
                    
                    b.nodes.forEach(node => {
                        // node.relX 已经是节点中心的相对坐标，直接加偏移即为绝对中心 X
                        node.x = b.absX + b.paddingX + node.relX;
                        node.y = b.absY + b.paddingY + 35 + node.relY + 25;
                    });
                });

                this.renderedModuleBoxes = moduleBoxes;
            } else {
                this.renderedModuleBoxes = [];
                // ===== 双向发散树形布局定位算法 =====
                const viewW = canvas.width || 1000;
                const viewH = canvas.height || 600;
                const nodeSpacingX = 260;
                const nodeSpacingY = 150;

                const rootNode = traceNodes.find(n => n.id === rootNodeId);
                if (rootNode) {
                    rootNode.x = viewW / 2;
                    rootNode.y = viewH / 2;
                }

                const assignXDown = (parentId) => {
                    const children = calleeMap[parentId] || [];
                    if (children.length === 0) return;
                    const pNode = traceNodes.find(n => n.id === parentId);
                    if (!pNode) return;
                    const cCount = children.length;
                    children.forEach((cId, idx) => {
                        const cNode = traceNodes.find(n => n.id === cId);
                        if (cNode) {
                            cNode.y = pNode.y + nodeSpacingY;
                            cNode.x = pNode.x + (idx - (cCount - 1) / 2) * nodeSpacingX;
                            assignXDown(cId);
                        }
                    });
                };
                assignXDown(rootNodeId);

                const assignXUp = (childId) => {
                    const parents = callerMap[childId] || [];
                    if (parents.length === 0) return;
                    const cNode = traceNodes.find(n => n.id === childId);
                    if (!cNode) return;
                    const pCount = parents.length;
                    parents.forEach((pId, idx) => {
                        const pNode = traceNodes.find(n => n.id === pId);
                        if (pNode) {
                            pNode.y = cNode.y - nodeSpacingY;
                            pNode.x = cNode.x + (idx - (pCount - 1) / 2) * nodeSpacingX;
                            assignXUp(pId);
                        }
                    });
                };
                assignXUp(rootNodeId);

                const layersY = {};
                traceNodes.forEach(node => {
                    const layerIdx = node._layer;
                    if (!layersY[layerIdx]) layersY[layerIdx] = [];
                    layersY[layerIdx].push(node);
                });

                Object.values(layersY).forEach(layer => {
                    if (layer.length <= 1) return;
                    for (let iter = 0; iter < 10; iter++) {
                        layer.sort((a, b) => a.x - b.x);
                        for (let i = 0; i < layer.length - 1; i++) {
                            const curr = layer[i];
                            const next = layer[i+1];
                            
                            const rightEdgeCurr = curr.x + (curr._nw || 220) / 2;
                            const leftEdgeNext = next.x - (next._nw || 220) / 2;
                            const actualOverlap = (rightEdgeCurr + 30) - leftEdgeNext;
                            if (actualOverlap > 0) {
                                curr.x -= actualOverlap / 2;
                                next.x += actualOverlap / 2;
                            }
                        }
                    }
                });

                if (traceNodes.length > 0) {
                    let minX = Infinity, maxX = -Infinity;
                    let minY = Infinity, maxY = -Infinity;
                    traceNodes.forEach(n => {
                        const w = n._nw || 220;
                        const h = 50;
                        if (n.x - w/2 < minX) minX = n.x - w/2;
                        if (n.x + w/2 > maxX) maxX = n.x + w/2;
                        if (n.y - h/2 < minY) minY = n.y - h/2;
                        if (n.y + h/2 > maxY) maxY = n.y + h/2;
                    });

                    const graphW = maxX - minX;
                    const graphH = maxY - minY;
                    const dx = (viewW - graphW) / 2 - minX;
                    const dy = (viewH - graphH) / 2 - minY;

                    traceNodes.forEach(n => {
                        n.x += dx;
                        n.y += dy;
                    });
                }
            }

            // 直接触发自适应缩放并重绘，不需要任何慢吞吞的力导向计算！
            this.fitToCanvas();
            this.renderGraph();
        },

        renderGraph() {
            if (!traceCanvas || !traceCtx) return;
            const ctx = traceCtx;
            const w = traceCanvas.width;
            const h = traceCanvas.height;

            ctx.save();

            // 缓存 CSS 变量与主题状态，仅在主题切换时刷新
            const bodyStyle = getComputedStyle(document.body);
            const bgColor1 = bodyStyle.getPropertyValue('--bg-color-1').trim() || '#0d111a';
            const bgColor2 = bodyStyle.getPropertyValue('--bg-color-2').trim() || '#1a1e36';
            const accent = bodyStyle.getPropertyValue('--accent-primary').trim();
            const fnColor = bodyStyle.getPropertyValue('--color-function').trim();

            if (accent) {
                COLORS.edge = accent.startsWith('#') ? hexToRgbA(accent, 0.4) : accent;
                COLORS.highlight = accent;
            }
            if (fnColor) {
                COLORS.function = fnColor;
            }

            const isLightTheme = document.body.classList.contains('theme-light');

            // 计算视口边界（变换空间坐标），用于裁剪不可见节点
            const k = traceXform.k;
            const tx = traceXform.x;
            const ty = traceXform.y;
            const margin = 80 / k;
            const viewLeft = -tx / k - margin;
            const viewRight = (w - tx) / k + margin;
            const viewTop = -ty / k - margin;
            const viewBottom = (h - ty) / k + margin;

            // 绘制背景色
            if (isLightTheme) {
                const radialGrad = ctx.createRadialGradient(w / 2, h / 2, 0, w / 2, h / 2, Math.max(w, h));
                radialGrad.addColorStop(0, '#ffffff');
                radialGrad.addColorStop(0.6, '#f0f2f7');
                radialGrad.addColorStop(1, '#e4e8f1');
                ctx.fillStyle = radialGrad;
                ctx.fillRect(0, 0, w, h);
            } else {
                ctx.fillStyle = bgColor1;
                ctx.fillRect(0, 0, w, h);
                const radialGrad = ctx.createRadialGradient(w / 2, h / 2, 0, w / 2, h / 2, Math.max(w, h));
                radialGrad.addColorStop(0, bgColor2);
                radialGrad.addColorStop(0.6, bgColor1);
                radialGrad.addColorStop(1, '#050508');
                ctx.fillStyle = radialGrad;
                ctx.fillRect(0, 0, w, h);
            }

            // 绘制细密的高科技 CAD 坐标网格
            ctx.save();
            ctx.translate(traceXform.x, traceXform.y);
            ctx.scale(traceXform.k, traceXform.k);

            ctx.strokeStyle = isLightTheme ? 'rgba(0, 0, 0, 0.04)' : 'rgba(99, 102, 241, 0.05)';
            ctx.lineWidth = 0.8;
            
            const gridSpacing = 40;
            const startGridX = Math.floor((-traceXform.x) / traceXform.k / gridSpacing) * gridSpacing - gridSpacing;
            const endGridX = startGridX + Math.ceil(w / traceXform.k / gridSpacing) * gridSpacing + gridSpacing * 2;
            const startGridY = Math.floor((-traceXform.y) / traceXform.k / gridSpacing) * gridSpacing - gridSpacing;
            const endGridY = startGridY + Math.ceil(h / traceXform.k / gridSpacing) * gridSpacing + gridSpacing * 2;

            for (let x = startGridX; x <= endGridX; x += gridSpacing) {
                ctx.beginPath();
                ctx.moveTo(x, startGridY);
                ctx.lineTo(x, endGridY);
                ctx.stroke();
            }
            for (let y = startGridY; y <= endGridY; y += gridSpacing) {
                ctx.beginPath();
                ctx.moveTo(startGridX, y);
                ctx.lineTo(endGridX, y);
                ctx.stroke();
            }
            ctx.restore();

            ctx.translate(traceXform.x, traceXform.y);
            ctx.scale(traceXform.k, traceXform.k);

            // Draw module group boxes if useGrouping is active
            if (useGrouping && this.renderedModuleBoxes) {
                this.renderedModuleBoxes.forEach(box => {
                    const rx = box.absX;
                    const ry = box.absY;
                    const rw = box.boxW;
                    const rh = box.boxH;
                    const radius = 12;

                    ctx.save();
                    
                    // Glassmorphic background
                    const boxGrad = ctx.createLinearGradient(rx, ry, rx, ry + rh);
                    if (isLightTheme) {
                        boxGrad.addColorStop(0, 'rgba(255, 255, 255, 0.8)');
                        boxGrad.addColorStop(1, 'rgba(249, 250, 251, 0.95)');
                    } else {
                        boxGrad.addColorStop(0, 'rgba(15, 23, 42, 0.45)');
                        boxGrad.addColorStop(1, 'rgba(15, 23, 42, 0.7)');
                    }
                    ctx.fillStyle = boxGrad;
                    
                    const levelColors = isLightTheme ? [
                        'rgba(225, 29, 72, 0.7)',    // L0: Rose
                        'rgba(249, 115, 22, 0.75)',  // L1: Orange
                        'rgba(13, 148, 136, 0.75)',  // L2: Teal
                        'rgba(79, 70, 229, 0.75)',   // L3: Indigo
                        'rgba(147, 51, 234, 0.75)',  // L4: Purple
                        'rgba(219, 39, 119, 0.7)',   // L5: Pink
                        'rgba(75, 85, 99, 0.6)'      // L6: Gray
                    ] : [
                        'rgba(245, 158, 11, 0.45)',  // 0: Gold
                        'rgba(249, 115, 22, 0.4)',   // 1: Orange
                        'rgba(14, 165, 233, 0.4)',   // 2: Cyan
                        'rgba(99, 102, 241, 0.4)',   // 3: Indigo
                        'rgba(168, 85, 247, 0.4)',   // 4: Purple
                        'rgba(236, 72, 153, 0.35)',  // 5: Pink (Unassigned)
                        'rgba(100, 116, 139, 0.3)'   // 6: Slate (External)
                    ];
                    const strokeColor = levelColors[box.level % levelColors.length];

                    // Draw rounded rect
                    ctx.beginPath();
                    ctx.moveTo(rx + radius, ry);
                    ctx.lineTo(rx + rw - radius, ry);
                    ctx.quadraticCurveTo(rx + rw, ry, rx + rw, ry + radius);
                    ctx.lineTo(rx + rw, ry + rh - radius);
                    ctx.quadraticCurveTo(rx + rw, ry + rh, rx + rw - radius, ry + rh);
                    ctx.lineTo(rx + radius, ry + rh);
                    ctx.quadraticCurveTo(rx, ry + rh, rx, ry + rh - radius);
                    ctx.lineTo(rx, ry + radius);
                    ctx.quadraticCurveTo(rx, ry, rx + radius, ry);
                    ctx.closePath();

                    ctx.shadowBlur = isLightTheme ? 6 : 10;
                    ctx.shadowColor = isLightTheme ? 'rgba(0,0,0,0.06)' : strokeColor;
                    ctx.strokeStyle = strokeColor;
                    ctx.lineWidth = 1.5;
                    ctx.fill();
                    ctx.stroke();
                    ctx.shadowBlur = 0; // reset

                    // Draw title background bar
                    ctx.fillStyle = isLightTheme ? 'rgba(0, 0, 0, 0.02)' : 'rgba(255, 255, 255, 0.02)';
                    ctx.beginPath();
                    ctx.moveTo(rx + 1, ry + 1);
                    ctx.lineTo(rx + rw - 1, ry + 1);
                    ctx.lineTo(rx + rw - 1, ry + 28);
                    ctx.lineTo(rx + 1, ry + 28);
                    ctx.closePath();
                    ctx.fill();
                    
                    // Draw bottom border for title bar
                    ctx.strokeStyle = isLightTheme ? 'rgba(0, 0, 0, 0.04)' : 'rgba(255, 255, 255, 0.05)';
                    ctx.lineWidth = 1;
                    ctx.beginPath();
                    ctx.moveTo(rx + 1, ry + 28);
                    ctx.lineTo(rx + rw - 1, ry + 28);
                    ctx.stroke();

                    // Draw title text
                    ctx.fillStyle = isLightTheme ? '#1f2937' : '#ffffff';
                    ctx.font = 'bold 11px "Inter", "Outfit", sans-serif';
                    ctx.textAlign = 'left';
                    ctx.fillText(box.moduleName, rx + 16, ry + 18);

                    // Draw level badge on top right of the box
                    ctx.fillStyle = strokeColor;
                    ctx.font = 'bold 9px "JetBrains Mono", monospace';
                    ctx.textAlign = 'right';
                    const levelLabel = box.level === 0 ? (window.i18n.locale === 'zh' ? 'L0 (最顶层)' : 'L0 (Top)') : `Level ${box.level}`;
                    ctx.fillText(`[${box.moduleId}]  ${levelLabel}`, rx + rw - 16, ry + 18);

                    // Draw horizontal dashed divider lines between calling layers
                    if (box.dividers && box.dividers.length > 0) {
                        ctx.strokeStyle = isLightTheme ? 'rgba(225, 29, 72, 0.35)' : 'rgba(239, 68, 68, 0.35)'; // 温暖微红高亮色
                        ctx.lineWidth = 1.25;
                        ctx.setLineDash([4, 4]); // Dashed style
                        box.dividers.forEach(divY => {
                            const y = ry + box.paddingY + 35 + divY;
                            ctx.beginPath();
                            ctx.moveTo(rx + 16, y);
                            ctx.lineTo(rx + rw - 16, y);
                            ctx.stroke();
                        });
                        ctx.setLineDash([]); // Reset dash pattern
                    }

                    ctx.restore();
                });
            }

            // Draw edges (smooth cubic bezier curves) with viewport culling
            traceLinks.forEach(l => {
                const src = typeof l.source === 'object' ? l.source : traceNodeMap.get(l.source);
                const tgt = typeof l.target === 'object' ? l.target : traceNodeMap.get(l.target);
                if (!src || !tgt || src.x == null || tgt.x == null) return;
                // 视口裁剪：边两端都在视口外则跳过
                const srcVis = src.x >= viewLeft && src.x <= viewRight && src.y >= viewTop && src.y <= viewBottom;
                const tgtVis = tgt.x >= viewLeft && tgt.x <= viewRight && tgt.y >= viewTop && tgt.y <= viewBottom;
                if (!srcVis && !tgtVis) return;

                const isImpact = l._impact;
                const isSelectedPath = isImpact || tgt.id === selectedNodeId || src.id === selectedNodeId;
                
                let color;
                if (isLightTheme) {
                    color = isSelectedPath ? '#e11d48' : 'rgba(0, 0, 0, 0.12)';
                } else {
                    color = isSelectedPath ? COLORS.impact : COLORS.edge;
                }

                const x1 = src.x;
                const y1 = src.y + 25; // 节点底部
                const x2 = tgt.x;
                const y2 = tgt.y - 25; // 节点顶部
                const midY = (y1 + y2) / 2;

                ctx.beginPath();
                ctx.strokeStyle = color;
                ctx.lineWidth = isSelectedPath ? 2.5 : 1.2;
                
                // Glow for active paths
                if (isSelectedPath) {
                    ctx.shadowColor = color;
                    ctx.shadowBlur = 8;
                } else {
                    ctx.shadowBlur = 0;
                }

                ctx.moveTo(x1, y1);
                ctx.bezierCurveTo(x1, midY, x2, midY, x2, y2);
                ctx.stroke();
                ctx.shadowBlur = 0; // reset

                // Arrow at target card top edge
                ctx.beginPath();
                ctx.fillStyle = color;
                ctx.moveTo(x2 - 5, y2 - 6);
                ctx.lineTo(x2 + 5, y2 - 6);
                ctx.lineTo(x2, y2);
                ctx.closePath();
                ctx.fill();
            });

            // Flow animation particles along bezier paths
            if (flowAnimFrame) {
                flowOffset = (flowOffset + 0.006) % 1;
                traceLinks.forEach(l => {
                    if (!l._impact) return;
                    const src = typeof l.source === 'object' ? l.source : traceNodes.find(n => n.id === l.source);
                    const tgt = typeof l.target === 'object' ? l.target : traceNodes.find(n => n.id === l.target);
                    if (!src || !tgt || src.x == null) return;

                    const x1 = src.x;
                    const y1 = src.y + 25;
                    const x2 = tgt.x;
                    const y2 = tgt.y - 25;
                    const midY = (y1 + y2) / 2;

                    const ct = (flowOffset + Math.random() * 0.08) % 1;
                    
                    // Cubic Bezier interpolation function
                    const bezierPoint = (p0, p1, p2, p3, t) => {
                        const mt = 1 - t;
                        return mt * mt * mt * p0 + 3 * mt * mt * t * p1 + 3 * mt * t * t * p2 + t * t * t * p3;
                    };

                    const px = bezierPoint(x1, x1, x2, x2, ct);
                    const py = bezierPoint(y1, midY, midY, y2, ct);

                    const grad = ctx.createRadialGradient(px, py, 0, px, py, 5);
                    grad.addColorStop(0, 'rgba(255, 42, 42, 1)');
                    grad.addColorStop(1, 'rgba(255, 42, 42, 0)');
                    ctx.beginPath();
                    ctx.fillStyle = grad;
                    ctx.arc(px, py, 5, 0, Math.PI * 2);
                    ctx.fill();
                });
            }

            // Draw nodes
            traceNodes.forEach(n => {
                if (n.x == null) return;
                // 视口裁剪：跳过不可见节点
                const nw = n._nw || 220;
                if (n.x + nw/2 < viewLeft || n.x - nw/2 > viewRight || n.y + 25 < viewTop || n.y - 25 > viewBottom) return;
                const isRoot = n.id === rootNodeId;
                const isSelected = n.id === selectedNodeId;
                const isUnknown = n.type === 'unknown';
                const isActive = isRoot || isSelected || n._impact;

                const nh = 50;  // Height
                const cx = n.x;
                const cy = n.y;
                const rx = cx - nw / 2;
                const ry = cy - nh / 2;
                const radius = 8;

                // 绘制圆角卡片路径
                ctx.beginPath();
                ctx.moveTo(rx + radius, ry);
                ctx.lineTo(rx + nw - radius, ry);
                ctx.quadraticCurveTo(rx + nw, ry, rx + nw, ry + radius);
                ctx.lineTo(rx + nw, ry + nh - radius);
                ctx.quadraticCurveTo(rx + nw, ry + nh, rx + nw - radius, ry + nh);
                ctx.lineTo(rx + radius, ry + nh);
                ctx.quadraticCurveTo(rx, ry + nh, rx, ry + nh - radius);
                ctx.lineTo(rx, ry + radius);
                ctx.quadraticCurveTo(rx, ry, rx + radius, ry);
                ctx.closePath();

                // 极具质感的线性渐变填充
                const bgGrad = ctx.createLinearGradient(rx, ry, rx, ry + nh);
                if (isLightTheme) {
                    if (n.type === 'virtual_aggregate') {
                        bgGrad.addColorStop(0, 'rgba(139, 92, 246, 0.08)');
                        bgGrad.addColorStop(1, 'rgba(139, 92, 246, 0.15)');
                    } else if (isRoot) {
                        bgGrad.addColorStop(0, '#fff1f2');
                        bgGrad.addColorStop(1, '#ffe4e6');
                    } else if (isSelected) {
                        bgGrad.addColorStop(0, '#f0fdf4');
                        bgGrad.addColorStop(1, '#dcfce7');
                    } else if (n._impact) {
                        bgGrad.addColorStop(0, '#fff1f2');
                        bgGrad.addColorStop(1, '#ffe4e6');
                    } else {
                        bgGrad.addColorStop(0, '#ffffff');
                        bgGrad.addColorStop(1, '#f9fafb');
                    }
                } else {
                    if (n.type === 'virtual_aggregate') {
                        bgGrad.addColorStop(0, 'rgba(139, 92, 246, 0.08)');
                        bgGrad.addColorStop(1, 'rgba(139, 92, 246, 0.18)');
                    } else if (isRoot) {
                        bgGrad.addColorStop(0, 'rgba(245, 158, 11, 0.15)');
                        bgGrad.addColorStop(1, 'rgba(217, 119, 6, 0.25)');
                    } else if (isSelected) {
                        bgGrad.addColorStop(0, 'rgba(56, 189, 248, 0.2)');
                        bgGrad.addColorStop(1, 'rgba(14, 165, 233, 0.3)');
                    } else if (n._impact || (showBlastRadius && _blastRadiusCache[n.id] > 0)) {
                        bgGrad.addColorStop(0, 'rgba(239, 68, 68, 0.15)');
                        bgGrad.addColorStop(1, 'rgba(220, 38, 38, 0.25)');
                    } else {
                        bgGrad.addColorStop(0, 'rgba(15, 23, 42, 0.85)');
                        bgGrad.addColorStop(1, 'rgba(30, 41, 59, 0.95)');
                    }
                }
                ctx.fillStyle = bgGrad;

                // 霓虹发光边框样式
                if (n.type === 'virtual_aggregate') {
                    ctx.shadowBlur = 0;
                    ctx.strokeStyle = isLightTheme ? 'rgba(139, 92, 246, 0.6)' : 'rgba(167, 139, 250, 0.5)';
                    ctx.lineWidth = 1.2;
                    ctx.setLineDash([4, 3]);
                } else if (isActive) {
                    ctx.setLineDash([]);
                    if (isLightTheme) {
                        ctx.shadowColor = isRoot ? 'rgba(225, 29, 72, 0.2)' : (n._impact ? 'rgba(225, 29, 72, 0.2)' : 'rgba(16, 185, 129, 0.2)');
                        ctx.shadowBlur = 6;
                        ctx.strokeStyle = isRoot ? '#e11d48' : (n._impact ? '#e11d48' : '#10b981');
                    } else {
                        ctx.shadowColor = isRoot ? 'rgba(245, 158, 11, 0.4)' : (n._impact ? 'rgba(239, 68, 68, 0.4)' : 'rgba(56, 189, 248, 0.4)');
                        ctx.shadowBlur = 12;
                        ctx.strokeStyle = isRoot ? '#f59e0b' : (n._impact ? '#ef4444' : '#38bdf8');
                    }
                    ctx.lineWidth = 2;
                } else {
                    ctx.setLineDash([]);
                    ctx.shadowBlur = 0;
                    ctx.strokeStyle = isUnknown ? (isLightTheme ? 'rgba(107, 114, 128, 0.3)' : 'rgba(100, 116, 139, 0.4)') : (isLightTheme ? 'rgba(0, 0, 0, 0.1)' : 'rgba(99, 102, 241, 0.3)');
                    ctx.lineWidth = 1.2;
                }

                ctx.fill();
                ctx.stroke();
                ctx.setLineDash([]); // 重置虚线样式
                ctx.shadowBlur = 0;  // 重置发光

                // --- 绘制首行：函数名称 ---
                if (n.type === 'virtual_aggregate') {
                    ctx.fillStyle = isLightTheme ? '#7c3aed' : '#c084fc';
                    ctx.textAlign = 'left';
                    ctx.font = 'bold 12px "Inter", "Outfit", sans-serif';
                    ctx.fillText('📁 ' + n.name, rx + 15, ry + 22);
                } else {
                    ctx.fillStyle = isLightTheme ? '#1f2937' : ((isRoot || isSelected) ? '#ffffff' : '#e2e8f0');
                    ctx.textAlign = 'left';
                    ctx.font = 'bold 12px "Inter", "Outfit", sans-serif';
                    let displayName = n.name.length > 40 ? n.name.substring(0, 37) + '...' : n.name;
                    if (n.isClone) {
                        displayName += ' 🔗';
                    }
                    ctx.fillText(displayName, rx + 15, ry + 22);
                }

                // --- 绘制次行：真实的源码文件归属与行号 ---
                if (n.type === 'virtual_aggregate') {
                    ctx.font = '500 9px "JetBrains Mono", monospace';
                    ctx.fillStyle = isLightTheme ? '#6d28d9' : '#a78bfa';
                    ctx.textAlign = 'left';
                    ctx.fillText(window.i18n.locale === 'zh' ? '点击右侧面板查看列表' : 'View list on right panel', rx + 15, ry + 38);
                } else {
                    const fileShort = n.file ? n.file.split('/').pop() : (isUnknown ? (window.i18n.locale === 'zh' ? '外部依赖' : 'External') : (window.i18n.locale === 'zh' ? '系统模块' : 'System Module'));
                    ctx.font = '500 9px "JetBrains Mono", monospace';
                    ctx.fillStyle = isLightTheme ? (isRoot ? '#e11d48' : (isUnknown ? '#6b7280' : '#059669')) : (isRoot ? '#f59e0b' : (isUnknown ? '#94a3b8' : '#38bdf8'));
                    ctx.textAlign = 'left';
                    ctx.fillText(fileShort, rx + 15, ry + 38);

                    if (n.line) {
                        ctx.font = '500 9px "JetBrains Mono", monospace';
                        ctx.fillStyle = isLightTheme ? 'rgba(0, 0, 0, 0.4)' : 'rgba(255, 255, 255, 0.4)';
                        ctx.textAlign = 'right';
                        ctx.fillText('L' + n.line, rx + nw - 15 - (showBlastRadius ? 10 : 0), ry + 38);
                    }
                }

                // 绘制复杂度热度色条
                const metrics = _cachedComplexity[n.id];
                if (metrics && metrics.cyclomatic_complexity) {
                    const cc = metrics.cyclomatic_complexity;
                    let ccColor = '#10b981';
                    if (cc > 20) ccColor = '#ef4444';
                    else if (cc > 10) ccColor = '#f59e0b';
                    
                    ctx.fillStyle = ccColor;
                    ctx.beginPath();
                    ctx.rect(rx + nw - 6, ry + 10, 3, 30);
                    ctx.fill();
                }

                // 绘制爆炸半径影响数量徽标
                if (showBlastRadius) {
                    const blastCount = _blastRadiusCache[n.id] || 0;
                    ctx.fillStyle = isLightTheme ? 'rgba(239, 68, 68, 0.15)' : 'rgba(239, 68, 68, 0.25)';
                    ctx.strokeStyle = '#ef4444';
                    ctx.lineWidth = 1;
                    const badgeText = `💣 +${blastCount}`;
                    ctx.font = 'bold 9px "JetBrains Mono", monospace';
                    const badgeW = ctx.measureText(badgeText).width + 8;
                    const badgeH = 13;
                    
                    const bx = rx + nw - badgeW - 12;
                    const by = ry + 8;
                    
                    ctx.beginPath();
                    ctx.rect(bx, by, badgeW, badgeH);
                    ctx.fill();
                    ctx.stroke();
                    
                    ctx.fillStyle = '#ef4444';
                    ctx.textAlign = 'center';
                    ctx.fillText(badgeText, bx + badgeW / 2, by + 9);
                }
            });

            ctx.restore();

            // Continue animation loop
            if (flowAnimFrame) {
                flowAnimFrame = requestAnimationFrame(() => this.renderGraph());
            }
        },

        toggleImpactFlow() {
            if (flowAnimFrame) {
                cancelAnimationFrame(flowAnimFrame);
                flowAnimFrame = null;
                traceLinks.forEach(l => l._impact = false);
                document.getElementById('und-btn-impact').classList.remove('active');
                this.renderGraph();
                return;
            }

            // Mark all edges from root as impact
            const impactSet = new Set();
            const visit = (id) => {
                if (impactSet.has(id)) return;
                impactSet.add(id);
                traceLinks.forEach(l => {
                    const src = typeof l.source === 'object' ? l.source.id : l.source;
                    const tgt = typeof l.target === 'object' ? l.target.id : l.target;
                    if (src === id) {
                        l._impact = true;
                        visit(tgt);
                    }
                });
            };
            visit(rootNodeId);

            document.getElementById('und-btn-impact').classList.add('active');
            flowAnimFrame = requestAnimationFrame(() => this.renderGraph());
        },

        toggleGrouping() {
            useGrouping = !useGrouping;
            const btnGroup = document.getElementById('und-btn-group');
            if (btnGroup) {
                if (useGrouping) btnGroup.classList.add('active');
                else btnGroup.classList.remove('active');
            }
            this.buildGraph();
        },

        fitToCanvas() {
            if (!traceCanvas || !traceNodes.length) return;
            let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
            
            if (useGrouping && this.renderedModuleBoxes && this.renderedModuleBoxes.length > 0) {
                this.renderedModuleBoxes.forEach(b => {
                    minX = Math.min(minX, b.absX);
                    maxX = Math.max(maxX, b.absX + b.boxW);
                    minY = Math.min(minY, b.absY);
                    maxY = Math.max(maxY, b.absY + b.boxH);
                });
            } else {
                traceNodes.forEach(n => {
                    if (n.x != null) {
                        minX = Math.min(minX, n.x);
                        maxX = Math.max(maxX, n.x);
                    }
                    if (n.y != null) {
                        minY = Math.min(minY, n.y);
                        maxY = Math.max(maxY, n.y);
                    }
                });
            }

            const pad = 60;
            const gw = (maxX - minX) || 100;
            const gh = (maxY - minY) || 100;
            const scale = Math.min(
                (traceCanvas.width - pad * 2) / gw,
                (traceCanvas.height - pad * 2) / gh,
                2
            ) * 0.85;

            const cx = (minX + maxX) / 2;
            const cy = (minY + maxY) / 2;
            const tx = traceCanvas.width / 2 - cx * scale;
            const ty = traceCanvas.height / 2 - cy * scale;

            traceXform = d3.zoomIdentity.translate(tx, ty).scale(scale);
            if (traceZoom) {
                d3.select(traceCanvas).call(traceZoom.transform, traceXform);
            }
            this.renderGraph();
        },

        showNodeInfo(node) {
            // Node info panel removed per user request
            // Keeping for potential future use
        },

        async loadCodePreview(node) {
            this.updateQualityPanel(node);
            const body = document.getElementById('und-code-body');
            const fileLabel = document.getElementById('und-code-file');
            const titleLabel = document.getElementById('und-code-title');
            if (!body) return;

            if (node.type === 'virtual_aggregate') {
                titleLabel.textContent = '📁 ' + node.name;
                fileLabel.textContent = '被折叠的辅助工具函数';
                
                let listHtml = `<div class="und-aggregate-list">`;
                listHtml += `<div class="und-agg-tip">为了防止调用图连线交叉错乱，以下低入度或纯依赖性质的辅助函数已被智能折叠。点击任意函数可预览其源码：</div>`;
                
                node.foldedNodeIds.forEach(fnId => {
                    const sym = window.G_DATA && window.G_DATA.symbols[fnId];
                    const displayName = sym ? sym.name : fnId;
                    const displayFile = (sym && sym.file) ? sym.file.split('/').pop() : '外部符号';
                    listHtml += `<div class="und-agg-item" onclick="window.SourceAstra.loadAggregaterSource('${fnId}')">` +
                        `<span>fn</span><strong>${displayName}</strong>` +
                        `<small>${displayFile}</small>` +
                        `</div>`;
                });
                listHtml += `</div>`;
                body.innerHTML = listHtml;
                return;
            }

            if (!node.file) {
                body.innerHTML = '<div class="und-code-empty"><div style="font-size:24px">⚠️</div><div>外部符号，无源码可用</div></div>';
                return;
            }

            titleLabel.textContent = '📄 ' + node.name;
            fileLabel.textContent = node.file + ':' + (node.line || '?');
            body.innerHTML = '<div class="und-code-loading">加载源码中...</div>';

            const jobId = sessionStorage.getItem('sourceastra_job_id');
            const url = `/api/snippet?file=${encodeURIComponent(node.file)}&line=${node.line || 1}&count=10000${jobId ? '&job=' + jobId : ''}`;

            try {
                const res = await fetch(url);
                const data = await res.json();
                if (data.error) throw new Error(data.error);

                if (data.fallback) {
                    // SCIP 模式下降级：源码文件不可达，展示符号元信息
                    let html = '<div class="und-code-fallback">';
                    html += '<div class="und-fallback-header">';
                    html += `<span class="und-fallback-type">${this.escapeHtml(data.type || 'function')}</span>`;
                    html += `<span class="und-fallback-name">${this.escapeHtml(data.name || node.name)}</span>`;
                    html += `<span class="und-fallback-loc">${this.escapeHtml(data.file)}:${data.line || '?'}</span>`;
                    if (data.module_id) html += `<span class="und-fallback-mod">📦 ${this.escapeHtml(data.module_id)}</span>`;
                    html += '</div>';
                    if (data.callers && data.callers.length > 0) {
                        html += '<div class="und-fallback-section"><div class="und-fallback-title">↑ 被调用 (' + data.callers.length + ')</div>';
                        data.callers.forEach(c => {
                            html += `<div class="und-fallback-item und-clickable" data-node-id="${this.escapeHtml(c.id)}">`;
                            html += `<span class="und-fi-name">${this.escapeHtml(c.name)}</span>`;
                            html += `<span class="und-fi-loc">${this.escapeHtml(c.file)}:${c.line}</span>`;
                            html += '</div>';
                        });
                        html += '</div>';
                    }
                    if (data.callees && data.callees.length > 0) {
                        html += '<div class="und-fallback-section"><div class="und-fallback-title">↓ 调用 (' + data.callees.length + ')</div>';
                        data.callees.forEach(c => {
                            html += `<div class="und-fallback-item und-clickable" data-node-id="${this.escapeHtml(c.id)}">`;
                            html += `<span class="und-fi-name">${this.escapeHtml(c.name)}</span>`;
                            html += `<span class="und-fi-loc">${this.escapeHtml(c.file)}:${c.line}</span>`;
                            html += '</div>';
                        });
                        html += '</div>';
                    }
                    if (!data.callers?.length && !data.callees?.length) {
                        html += '<div class="und-fallback-empty">该函数无调用关系记录</div>';
                    }
                    html += '</div>';
                    body.innerHTML = html;
                    // 点击调用者/被调用者跳转
                    body.querySelectorAll('.und-clickable').forEach(el => {
                        el.onclick = () => {
                            const nid = el.getAttribute('data-node-id');
                            if (nid) this._drawTrace(nid);
                        };
                    });
                } else if (data.snippet && data.snippet.length > 0) {
                    let html = '';
                    data.snippet.forEach((line, i) => {
                        const lineNum = data.start + i;
                        const isHighlight = lineNum === node.line;
                        html += `<div class="und-code-line ${isHighlight ? 'und-code-hl' : ''}">` +
                            `<span class="und-line-num">${lineNum}</span>` +
                            `<span class="und-line-code">${this.escapeHtml(line)}</span>` +
                            `</div>`;
                    });
                    body.innerHTML = html;

                    // Scroll to highlighted line
                    const hlLine = body.querySelector('.und-code-hl');
                    if (hlLine) hlLine.scrollIntoView({ block: 'center', behavior: 'smooth' });
                } else {
                    body.innerHTML = '<div class="und-code-empty">无预览可用</div>';
                }
            } catch (e) {
                body.innerHTML = '<div class="und-code-empty">加载失败: ' + e.message + '</div>';
            }
        },

        async updateQualityPanel(node) {
            const panel = document.getElementById('und-quality-panel');
            if (!panel) return;
            if (!node || node.type === 'virtual_aggregate' || !node.file) {
                panel.style.display = 'none';
                return;
            }

            panel.style.display = 'flex';
            panel.innerHTML = `<div style="font-size:11px;color:rgba(255,255,255,0.4)">加载质量指标中...</div>`;

            try {
                let metrics = _cachedComplexity[node.id];
                if (!metrics) {
                    const res = await fetch('/api/complexity/calculate', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ symbol_id: node.id })
                    });
                    if (res.ok) {
                        const results = await res.json();
                        if (Array.isArray(results) && results.length > 0) {
                            metrics = results[0];
                            _cachedComplexity[node.id] = metrics;
                        }
                    }
                }

                if (metrics) {
                    const cc = metrics.cyclomatic_complexity || 0;
                    let ccBadge = 'green';
                    let ccLabel = '🟢 偏低';
                    if (cc > 20) { ccBadge = 'red'; ccLabel = '🔴 极高'; }
                    else if (cc > 10) { ccBadge = 'yellow'; ccLabel = '🟡 较高'; }

                    const loc = metrics.lines_of_code || 0;
                    const depth = metrics.nesting_depth || 0;
                    const returnCount = metrics.return_count || 0;

                    panel.innerHTML = `
                        <div class="und-qp-item">
                            <span class="und-qp-label">圈复杂度</span>
                            <div class="und-qp-value-wrap">
                                <span class="und-qp-value">${cc}</span>
                                <span class="und-qp-badge ${ccBadge}">${ccLabel}</span>
                            </div>
                        </div>
                        <div class="und-qp-item">
                            <span class="und-qp-label">代码行数</span>
                            <div class="und-qp-value-wrap">
                                <span class="und-qp-value">${loc}</span>
                            </div>
                        </div>
                        <div class="und-qp-item">
                            <span class="und-qp-label">嵌套深度</span>
                            <div class="und-qp-value-wrap">
                                <span class="und-qp-value">${depth}</span>
                            </div>
                        </div>
                        <div class="und-qp-item">
                            <span class="und-qp-label">返回点数</span>
                            <div class="und-qp-value-wrap">
                                <span class="und-qp-value">${returnCount}</span>
                            </div>
                        </div>
                    `;
                } else {
                    panel.innerHTML = `<div style="font-size:11px;color:rgba(255,255,255,0.4)">暂无复杂度度量数据</div>`;
                }
            } catch (err) {
                panel.innerHTML = `<div style="font-size:11px;color:#ef4444">度量失败: ${err.message}</div>`;
            }
        },

        toggleBlastRadius() {
            if (showBlastRadius) {
                showBlastRadius = false;
                _blastRadiusCache = {};
                document.getElementById('und-btn-blast').classList.remove('active');
                this.renderGraph();
                return;
            }

            showBlastRadius = true;
            _blastRadiusCache = {};

            traceNodes.forEach(node => {
                const visited = new Set();
                const queue = [node.id];
                while(queue.length > 0) {
                    const cur = queue.shift();
                    if (visited.has(cur)) continue;
                    visited.add(cur);

                    traceLinks.forEach(l => {
                        const src = typeof l.source === 'object' ? l.source.id : l.source;
                        const tgt = typeof l.target === 'object' ? l.target.id : l.target;
                        if (tgt === cur) {
                            queue.push(src);
                        }
                    });
                }
                visited.delete(node.id);
                _blastRadiusCache[node.id] = visited.size;
            });

            document.getElementById('und-btn-blast').classList.add('active');
            this.renderGraph();
        },

        escapeHtml(str) {
            return str.replace(/[&<>"']/g, m => ({
                '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#039;'
            })[m]);
        },

        applyFilters() {
            // 声明/接口文件过滤规则：非起点节点不参与默认调用流展示。
            const isDeclarationFileNode = (n) => {
                if (n.id === rootNodeId) return false;
                if (!n.file) return false;
                return window.isSourceDeclarationFile && window.isSourceDeclarationFile(n.file);
            };

            if (showSystem) {
                traceNodes = rawNodes.filter(n => !isDeclarationFileNode(n)).map(n => ({ ...n }));
                const nodeIds = new Set(traceNodes.map(n => n.id));
                traceLinks = rawLinks.filter(l => {
                    const src = typeof l.source === 'object' ? l.source.id : l.source;
                    const tgt = typeof l.target === 'object' ? l.target.id : l.target;
                    return nodeIds.has(src) && nodeIds.has(tgt);
                }).map(l => ({ ...l }));
            } else {
                // 默认只保留非未知节点且不是声明/接口文件节点的函数
                traceNodes = rawNodes.filter(n => (n.id === rootNodeId || n.name === rootNodeId || n.type !== 'unknown') && !isDeclarationFileNode(n)).map(n => ({ ...n }));
                const nodeIds = new Set(traceNodes.map(n => n.id));
                traceLinks = rawLinks.filter(l => {
                    const src = typeof l.source === 'object' ? l.source.id : l.source;
                    const tgt = typeof l.target === 'object' ? l.target.id : l.target;
                    return nodeIds.has(src) && nodeIds.has(tgt);
                }).map(l => ({ ...l }));
            }

            this.pruneAndCloneGraph();

            // 1. 构建完整图的双向邻接表，并计算所有节点在调用树中的拓扑深度 _layer (基于未过滤的完整调用流关系)
            const calleeMap = {};
            const callerMap = {};
            traceLinks.forEach(l => {
                const src = typeof l.source === 'object' ? l.source.id : l.source;
                const tgt = typeof l.target === 'object' ? l.target.id : l.target;
                if (!calleeMap[src]) calleeMap[src] = [];
                if (!calleeMap[src].includes(tgt)) calleeMap[src].push(tgt);
                if (!callerMap[tgt]) callerMap[tgt] = [];
                if (!callerMap[tgt].includes(src)) callerMap[tgt].push(src);
            });

            const depthMap = {};
            depthMap[rootNodeId] = 0;

            const visitedDown = new Set([rootNodeId]);
            const queueDown = [rootNodeId];
            while (queueDown.length > 0) {
                const curr = queueDown.shift();
                const children = calleeMap[curr] || [];
                children.forEach(c => {
                    if (!visitedDown.has(c)) {
                        visitedDown.add(c);
                        depthMap[c] = depthMap[curr] + 1;
                        queueDown.push(c);
                    }
                });
            }

            const visitedUp = new Set([rootNodeId]);
            const queueUp = [rootNodeId];
            while (queueUp.length > 0) {
                const curr = queueUp.shift();
                const parents = callerMap[curr] || [];
                parents.forEach(p => {
                    if (!visitedUp.has(p)) {
                        visitedUp.add(p);
                        depthMap[p] = depthMap[curr] - 1;
                        queueUp.push(p);
                    }
                });
            }

            traceNodes.forEach(n => {
                n._layer = depthMap[n.id] !== undefined ? depthMap[n.id] : 0;
            });

            // 预先计算目录和层级关系
            traceNodes.forEach(n => {
                const group = directoryGroupForNode(n);
                n._groupModuleId = group.id;
                n._groupModuleName = group.name;
                n._groupLevel = group.level;
            });

            // 虚拟聚合节点继承父节点的目录属性
            traceNodes.forEach(n => {
                if (n.type === 'virtual_aggregate' && n.parentId) {
                    const parent = traceNodes.find(p => p.id === n.parentId);
                    if (parent) {
                        n._groupModuleId = parent._groupModuleId;
                        n._groupModuleName = parent._groupModuleName;
                        n._groupLevel = parent._groupLevel;
                    }
                }
            });

            // 动态填充目录透视过滤下拉菜单
            const moduleSelect = document.getElementById('und-module-select');
            if (moduleSelect) {
                const currentSelected = selectedModuleFilter || 'all';
                moduleSelect.innerHTML = '<option value="all">全部目录</option>';
                
                const uniqueModules = new Set();
                traceNodes.forEach(n => {
                    if (n._groupModuleId) {
                        uniqueModules.add(n._groupModuleId);
                    }
                });

                const sortedModules = Array.from(uniqueModules).sort();
                sortedModules.forEach(modId => {
                    const option = document.createElement('option');
                    option.value = modId;
                    option.textContent = modId;
                    moduleSelect.appendChild(option);
                });

                if (uniqueModules.has(currentSelected)) {
                    moduleSelect.value = currentSelected;
                    selectedModuleFilter = currentSelected;
                } else {
                    moduleSelect.value = 'all';
                    selectedModuleFilter = 'all';
                }

                // 若处于透视状态，高亮显示下拉菜单
                if (selectedModuleFilter !== 'all') {
                    moduleSelect.style.borderColor = 'var(--accent-primary)';
                    moduleSelect.style.color = 'var(--accent-primary)';
                    moduleSelect.style.background = 'rgba(99, 102, 241, 0.15)';
                } else {
                    moduleSelect.style.borderColor = '';
                    moduleSelect.style.color = '';
                    moduleSelect.style.background = '';
                }
            }

            // 过滤不属于选定目录的节点和连线
            if (selectedModuleFilter !== 'all') {
                traceNodes = traceNodes.filter(n => n._groupModuleId === selectedModuleFilter);
                const activeNodeIds = new Set(traceNodes.map(n => n.id));
                traceLinks = traceLinks.filter(l => {
                    const src = typeof l.source === 'object' ? l.source.id : l.source;
                    const tgt = typeof l.target === 'object' ? l.target.id : l.target;
                    return activeNodeIds.has(src) && activeNodeIds.has(tgt);
                });
            }
        },

        pruneAndCloneGraph() {
            if (!traceNodes.length) return;

            const rootId = rootNodeId;

            // 1. 统计各个过滤后节点的入度和出度
            const inDegree = {};
            const outDegree = {};
            traceNodes.forEach(n => {
                inDegree[n.id] = 0;
                outDegree[n.id] = 0;
            });
            traceLinks.forEach(l => {
                const src = typeof l.source === 'object' ? l.source.id : l.source;
                const tgt = typeof l.target === 'object' ? l.target.id : l.target;
                inDegree[tgt] = (inDegree[tgt] || 0) + 1;
                outDegree[src] = (outDegree[src] || 0) + 1;
            });

            // 2. 找出所有“公共叶子节点”（即入度 > 1，且出度 === 0 的外部依赖或辅助工具函数）
            const commonLeaves = new Set();
            traceNodes.forEach(n => {
                if (n.id !== rootId && inDegree[n.id] > 1 && outDegree[n.id] === 0) {
                    commonLeaves.add(n.id);
                }
            });

            // 3. 克隆公共叶子节点进行“局部化”解耦，消除大量交叉长线
            const finalNodes = [];
            const finalLinks = [];

            const commonLeafNodesMap = {};
            traceNodes.forEach(n => {
                if (commonLeaves.has(n.id)) {
                    commonLeafNodesMap[n.id] = n;
                } else {
                    finalNodes.push(n);
                }
            });

            traceLinks.forEach(l => {
                const src = typeof l.source === 'object' ? l.source.id : l.source;
                const tgt = typeof l.target === 'object' ? l.target.id : l.target;

                if (commonLeaves.has(tgt)) {
                    const originalLeaf = commonLeafNodesMap[tgt];
                    if (originalLeaf) {
                        const cloneId = originalLeaf.id + '_clone_' + src;
                        if (!finalNodes.some(n => n.id === cloneId)) {
                            finalNodes.push({
                                ...originalLeaf,
                                id: cloneId,
                                isClone: true,
                                originalId: originalLeaf.id
                            });
                        }
                        finalLinks.push({
                            source: src,
                            target: cloneId,
                            _impact: l._impact
                        });
                    }
                } else {
                    finalLinks.push({
                        source: src,
                        target: tgt,
                        _impact: l._impact
                    });
                }
            });

            // 4. 对每个父节点的叶子节点进行“智能收敛聚合” (叶子数 > 3 时折叠)
            const parentToLeaves = {};
            const currentOutDegree = {};
            finalNodes.forEach(n => { currentOutDegree[n.id] = 0; });
            finalLinks.forEach(l => {
                currentOutDegree[l.source] = (currentOutDegree[l.source] || 0) + 1;
            });

            finalLinks.forEach(l => {
                const parent = l.source;
                const child = l.target;
                const childNode = finalNodes.find(n => n.id === child);
                
                if (childNode && child !== rootId && currentOutDegree[child] === 0) {
                    if (!parentToLeaves[parent]) parentToLeaves[parent] = [];
                    parentToLeaves[parent].push(childNode);
                }
            });

            const nodesToKeep = new Set(finalNodes.map(n => n.id));
            const nodesToRemove = new Set();
            const virtualAggregates = [];

            Object.keys(parentToLeaves).forEach(parentId => {
                const leaves = parentToLeaves[parentId];
                if (leaves.length > 3) {
                    const keepCount = 2;
                    const leavesToRemove = leaves.slice(keepCount);
                    leavesToRemove.forEach(leaf => {
                        nodesToRemove.add(leaf.id);
                        nodesToKeep.delete(leaf.id);
                    });

                    const aggregateId = parentId + '_folded_leaves';
                    const foldedCount = leavesToRemove.length;
                    virtualAggregates.push({
                        id: aggregateId,
                        name: `${foldedCount} 个辅助函数`,
                        type: 'virtual_aggregate',
                        file: '',
                        line: 0,
                        _nw: 180,
                        parentId: parentId,
                        foldedNodeIds: leavesToRemove.map(l => l.originalId || l.id)
                    });
                    
                    finalLinks.push({
                        source: parentId,
                        target: aggregateId,
                        _impact: false
                    });
                }
            });

            traceNodes = finalNodes.filter(n => nodesToKeep.has(n.id)).concat(virtualAggregates);
            const activeNodeIds = new Set(traceNodes.map(n => n.id));
            traceLinks = finalLinks.filter(l => activeNodeIds.has(l.source) && activeNodeIds.has(l.target));
        },

        saveCurrentPositions() {
            const posMap = {};
            traceNodes.forEach(n => {
                if (n.x != null && n.y != null) {
                    posMap[n.id] = { x: n.x, y: n.y, vx: n.vx, vy: n.vy };
                }
            });
            rawNodes.forEach(n => {
                if (posMap[n.id]) {
                    n.x = posMap[n.id].x;
                    n.y = posMap[n.id].y;
                    n.vx = posMap[n.id].vx;
                    n.vy = posMap[n.id].vy;
                }
            });
        },

        toggleSystemFunctions() {
            this.saveCurrentPositions();
            showSystem = !showSystem;
            
            const btnDetail = document.getElementById('und-btn-detail');
            if (btnDetail) {
                if (showSystem) btnDetail.classList.add('active');
                else btnDetail.classList.remove('active');
            }

            this.applyFilters();
            this.buildGraph();
        },

        initResizer() {
            const self = this;
            const resizer = document.getElementById('und-resizer');
            const codePanel = document.querySelector('.und-code-panel');
            if (!resizer || !codePanel) return;

            let startX = 0, startWidth = 0;

            const onMouseMove = (e) => {
                const dx = e.clientX - startX;
                const newW = Math.max(200, Math.min(window.innerWidth - 300, startWidth - dx));
                codePanel.style.width = newW + 'px';
                
                if (traceCanvas) {
                    self.resizeTraceCanvas();
                    self.renderGraph();
                }
            };

            const onMouseUp = () => {
                resizer.classList.remove('dragging');
                document.body.classList.remove('resizing');
                document.removeEventListener('mousemove', onMouseMove);
                document.removeEventListener('mouseup', onMouseUp);
            };

            resizer.addEventListener('mousedown', (e) => {
                e.preventDefault();
                startX = e.clientX;
                startWidth = codePanel.offsetWidth;
                resizer.classList.add('dragging');
                document.body.classList.add('resizing');
                document.addEventListener('mousemove', onMouseMove);
                document.addEventListener('mouseup', onMouseUp);
            });

            window.addEventListener('resize', () => {
                if (window.G_CURRENT_VIEW === 'trace' && traceCanvas) {
                    self.resizeTraceCanvas();
                    self.buildGraph();
                    self.renderGraph();
                }
            });

            // 初始化源码面板折叠/展开
            const toggleBtn = document.getElementById('und-code-toggle');
            const layout = document.querySelector('.und-layout');
            if (toggleBtn && layout) {
                toggleBtn.addEventListener('click', (e) => {
                    e.stopPropagation();
                    const collapsed = layout.classList.toggle('code-collapsed');
                    toggleBtn.textContent = collapsed ? '‹' : '›';
                    toggleBtn.title = collapsed ? '展开源码预览' : '收起源码预览';
                    
                    // 重新计算并缩放画布
                    setTimeout(() => {
                        if (traceCanvas) {
                            self.resizeTraceCanvas();
                            self.buildGraph();
                            self.renderGraph();
                        }
                    }, 300);
                });
            }
        },

        resizeTraceCanvas() {
            if (!traceCanvas) return;
            const rect = traceCanvas.getBoundingClientRect();
            traceCanvas.width = Math.max(100, Math.floor(rect.width));
            traceCanvas.height = Math.max(100, Math.floor(rect.height));
        },
    };

    window.SourceAstra = window.SourceAstra || {};
    window.SourceAstra.initUnderstand = (nodeOrId) => Understand.init(nodeOrId);
    window.SourceAstra.loadAggregaterSource = (fnId) => {
        const sym = window.G_DATA && window.G_DATA.symbols[fnId];
        if (sym) {
            Understand.loadCodePreview(sym);
        }
    };
    window.SourceAstra.getModuleIdForNode = (nodeId) => {
        const node = traceNodes.find(n => n.id === nodeId);
        return node ? node._groupModuleId : null;
    };
    window.SourceAstra.getModuleNameForNode = (nodeId) => {
        const node = traceNodes.find(n => n.id === nodeId);
        return node ? node._groupModuleName : null;
    };
    window.SourceAstra.isolateModule = (moduleId) => {
        selectedModuleFilter = moduleId;
        
        // 同步更新界面上的目录选择下拉菜单的值
        const moduleSelect = document.getElementById('und-module-select');
        if (moduleSelect) moduleSelect.value = moduleId;

        Understand.saveCurrentPositions();
        Understand.applyFilters();
        Understand.buildGraph();
        Understand.renderGraph();
    };
    window.SourceAstra.getCurrentModuleFilter = () => {
        return selectedModuleFilter;
    };
    window.SourceAstra.clearModuleIsolation = () => {
        selectedModuleFilter = 'all';
        
        // 同步更新界面上的目录选择下拉菜单的值为"all"
        const moduleSelect = document.getElementById('und-module-select');
        if (moduleSelect) moduleSelect.value = 'all';

        Understand.saveCurrentPositions();
        Understand.applyFilters();
        Understand.buildGraph();
        Understand.renderGraph();
    };
    window.SourceAstra.resizeUnderstand = () => {
        if (traceCanvas) {
            Understand.resizeTraceCanvas();
            Understand.buildGraph();
            Understand.renderGraph();
        }
    };
    window.SourceAstra.refreshFunctionTree = () => {
        Understand.renderFunctionTree(document.getElementById('und-function-search')?.value || '');
    };

    window.addEventListener('sa-locale-changed', () => {
        if (window.G_CURRENT_VIEW === 'trace' && traceCanvas) {
            Understand.renderGraph();
        }
    });

    window.addEventListener('sa-theme-changed', () => {
        if (window.G_CURRENT_VIEW === 'trace' && traceCanvas) {
            Understand.renderGraph();
        }
    });
})();
