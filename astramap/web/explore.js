/**
 * SourceAstra - Explore Module JS v26
 * 两层导航架构：概览层(目录聚合大卡片) + 详情层(函数节点展开)
 * 微服务卡片风格：圆角矩形 + 金色标题 + 虚线连接 + 深色背景
 */

(function () {
    "use strict";
    const d3 = window.d3;

    // ── 主题色（全面升级对齐星云图：深靛蓝背景 + 极客酷色调）──
    const THEME = {
        bg: "transparent",                          // 让底层的 radial-gradient 极客背景透露出来
        cardBg: "url(#card-bg-grad)",               // 与追踪视图、星云图统一的深靛蓝背景，渐变形式提升对比度
        cardBorder: "url(#card-border-grad)",       // 半透明渐变微光边框
        cardBorderHover: "#38bdf8",                 // 悬浮态霓虹青色
        titleColor: "#f1f5f9",                      // 纯白高亮标题
        descColor: "#94a3b8",                       // 优雅的石板灰描述
        tagColor: "#cbd5e1",                        // 标签文字色
        tagBg: "rgba(99, 102, 241, 0.15)",         // 标签半透明蓝底
        linkColor: "rgba(255, 255, 255, 0.08)",    // 默认连线：与星云图一致的超细微半透明白线
        linkHighlight: "rgba(56, 189, 248, 0.85)", // 激活态连线：霓虹青色 (Cyan)
        separatorColor: "rgba(99, 102, 241, 0.15)",// 卡片内部分割线
        separatorColor2: "rgba(99, 102, 241, 0.1)",
        breadcrumbBg: "rgba(15, 23, 42, 0.85)",    // 面包屑背景
        breadcrumbActive: "#38bdf8",               // 激活态面包屑 (Cyan)
        textDim: "#64748b",
        circleNode: "#38bdf8",                     // 节点圆圈颜色 (Cyan)
        lightTextColor: "#cbd5e1",
    };

    function updateThemeColors() {
        const isLight = document.body.classList.contains('theme-light');
        if (isLight) {
            Object.assign(THEME, {
                bg: "transparent",
                cardBg: "url(#card-bg-grad)",
                cardBorder: "url(#card-border-grad)",
                cardBorderHover: "#6366f1",
                titleColor: "#1e293b",
                descColor: "#475569",
                tagColor: "#6366f1",
                tagBg: "rgba(99, 102, 241, 0.09)",
                linkColor: "rgba(100, 116, 139, 0.15)",
                linkHighlight: "rgba(99, 102, 241, 0.85)",
                separatorColor: "rgba(100, 116, 139, 0.08)",
                separatorColor2: "rgba(100, 116, 139, 0.06)",
                breadcrumbBg: "rgba(255, 255, 255, 0.88)",
                breadcrumbActive: "#6366f1",
                textDim: "#94a3b8",
                circleNode: "#6366f1",
                lightTextColor: "#475569",
            });
        } else {
            Object.assign(THEME, {
                bg: "transparent",
                cardBg: "url(#card-bg-grad)",
                cardBorder: "url(#card-border-grad)",
                cardBorderHover: "#38bdf8",
                titleColor: "#f1f5f9",
                descColor: "#94a3b8",
                tagColor: "#cbd5e1",
                tagBg: "rgba(99, 102, 241, 0.15)",
                linkColor: "rgba(255, 255, 255, 0.08)",
                linkHighlight: "rgba(56, 189, 248, 0.85)",
                separatorColor: "rgba(99, 102, 241, 0.15)",
                separatorColor2: "rgba(99, 102, 241, 0.1)",
                breadcrumbBg: "rgba(15, 23, 42, 0.85)",
                breadcrumbActive: "#38bdf8",
                textDim: "#64748b",
                circleNode: "#38bdf8",
                lightTextColor: "#cbd5e1",
            });
        }
    }

    // ── 卡片尺寸 ──
    const CARD = {
        width: 220,
        headerHeight: 32,
        descLineHeight: 16,
        entityLineHeight: 15,
        tagHeight: 20,
        padding: 12,
        radius: 10,
    };

    class ExploreView {
        constructor(containerId) {
            this.container = document.getElementById(containerId);
            this.svg = null;
            this.width = 0;
            this.height = 0;
            this.rawData = null;
            this.simulation = null;
            this.zoom = null;
            this.g = null;
            this.nodes = [];
            this.links = [];
            this.linkElements = null;
            this.nodeElements = null;
            this.labelElements = null;

            // 两层导航状态
            this.currentLevel = 'top'; // 'top' | 'module' | 'file' | 'trace'
            this.currentDir = null;
            this.currentFile = null;
            this.currentFuncId = null;
            this.breadcrumb = [];
            this.fileMap = null;
            this.fileEdges = null;

            // 聚合数据缓存
            this.dirMap = null;
            this.dirEdges = null;
            this.allNodesMap = null;
            this.docGenerating = false;

            window._exploreView = this;
            window.SourceAstra = window.SourceAstra || {};
            window.SourceAstra.openDocForExplore = (type, key, options) => {
                this.openDocDrawer(type, key, options || {});
            };

            this.initLayout();
            this.ensureDocDrawerStyle();
            this.bindEvents();
        }

        initLayout() {
            this.container.innerHTML = `
                <div id="explore-toolbar" style="position:absolute;top:12px;left:12px;z-index:10;display:flex;gap:8px;align-items:center;">
                    <span style="color:${THEME.textDim};font-size:11px;" id="explore-stats"></span>
                </div>
                
                <div id="explore-canvas-container"></div>
                <div id="explore-info"></div>

                <div id="explore-doc-drawer" class="explore-doc-drawer">
                    <div class="explore-doc-resizer" id="explore-doc-resizer"></div>
                    <div class="explore-doc-header">
                        <span class="explore-doc-title" id="explore-doc-title">理解文档</span>
                        <button class="explore-doc-close" id="explore-doc-close">✕</button>
                    </div>
                    <div class="explore-doc-content-area" id="explore-doc-content"></div>
                </div>
            `;
            this.canvasContainer = this.container.querySelector('#explore-canvas-container');
            this.infoPanel = this.container.querySelector('#explore-info');
            this.statsEl = this.container.querySelector('#explore-stats');
            this.breadcrumbEl = document.getElementById('explore-breadcrumb');

            this.docDrawer = this.container.querySelector('#explore-doc-drawer');
            this.docBtnToggle = null;
            this.docTitle = this.container.querySelector('#explore-doc-title');
            this.docContent = this.container.querySelector('#explore-doc-content');
            this.docCloseBtn = this.container.querySelector('#explore-doc-close');

            this.initDocDrawer();
            this.updateSize();
        }
        updateSize() {
            if (!this.container) return;
            // 使用container（view-explore）的高度，而不是canvasContainer（会被SVG撑大）
            const parentRect = this.container.getBoundingClientRect();
            let w = Math.max(100, parentRect.width || window.innerWidth);
            let h = Math.max(100, parentRect.height || window.innerHeight - 88);

            updateThemeColors();

            if (this.width === w && this.height === h && this.svg) return;

            this.width = w;
            this.height = h;

            d3.select(this.canvasContainer).selectAll('svg').remove();

            this.svg = d3.select(this.canvasContainer)
                .append('svg')
                .attr('width', '100%')
                .attr('height', '100%')
                .attr('viewBox', `0 0 ${this.width} ${this.height}`)
                .style('background', THEME.bg);

            const defs = this.svg.append('defs');

            // ── 技术点阵网格背景 ──
            const gridPattern = defs.append('pattern')
                .attr('id', 'tech-grid')
                .attr('width', 60)
                .attr('height', 60)
                .attr('patternUnits', 'userSpaceOnUse');

            gridPattern.append('path')
                .attr('d', 'M 60 0 L 0 0 0 60')
                .attr('fill', 'none')
                .attr('stroke', document.body.classList.contains('theme-light') ? 'rgba(139, 126, 102, 0.08)' : 'rgba(99, 102, 241, 0.08)')
                .attr('stroke-width', 0.8);

            gridPattern.append('circle')
                .attr('cx', 0)
                .attr('cy', 0)
                .attr('r', 1.5)
                .attr('fill', document.body.classList.contains('theme-light') ? 'rgba(79, 70, 229, 0.15)' : 'rgba(56, 189, 248, 0.25)');

            // ── 卡片常规背景渐变 ──
            const cardBgGrad = defs.append('linearGradient')
                .attr('id', 'card-bg-grad')
                .attr('x1', '0%').attr('y1', '0%')
                .attr('x2', '100%').attr('y2', '100%');
            if (document.body.classList.contains('theme-light')) {
                cardBgGrad.append('stop').attr('offset', '0%').attr('stop-color', '#fdfcfa');
                cardBgGrad.append('stop').attr('offset', '100%').attr('stop-color', '#f4f0e6');
            } else {
                cardBgGrad.append('stop').attr('offset', '0%').attr('stop-color', '#242b3d');
                cardBgGrad.append('stop').attr('offset', '100%').attr('stop-color', '#121724');
            }

            // ── 卡片激活（起点）背景渐变 ──
            const cardBgGradActive = defs.append('linearGradient')
                .attr('id', 'card-bg-grad-active')
                .attr('x1', '0%').attr('y1', '0%')
                .attr('x2', '100%').attr('y2', '100%');
            if (document.body.classList.contains('theme-light')) {
                cardBgGradActive.append('stop').attr('offset', '0%').attr('stop-color', '#e0dcfa');
                cardBgGradActive.append('stop').attr('offset', '100%').attr('stop-color', '#c7c3f0');
            } else {
                cardBgGradActive.append('stop').attr('offset', '0%').attr('stop-color', '#312e81');
                cardBgGradActive.append('stop').attr('offset', '100%').attr('stop-color', '#1e1b4b');
            }

            // ── 卡片常规微光渐变边框 ──
            const cardBorderGrad = defs.append('linearGradient')
                .attr('id', 'card-border-grad')
                .attr('x1', '0%').attr('y1', '0%')
                .attr('x2', '100%').attr('y2', '100%');
            if (document.body.classList.contains('theme-light')) {
                cardBorderGrad.append('stop').attr('offset', '0%').attr('stop-color', 'rgba(79, 70, 229, 0.35)');
                cardBorderGrad.append('stop').attr('offset', '40%').attr('stop-color', 'rgba(99, 102, 241, 0.2)');
                cardBorderGrad.append('stop').attr('offset', '100%').attr('stop-color', 'rgba(79, 70, 229, 0.12)');
            } else {
                cardBorderGrad.append('stop').attr('offset', '0%').attr('stop-color', 'rgba(56, 189, 248, 0.85)');
                cardBorderGrad.append('stop').attr('offset', '40%').attr('stop-color', 'rgba(99, 102, 241, 0.55)');
                cardBorderGrad.append('stop').attr('offset', '100%').attr('stop-color', 'rgba(56, 189, 248, 0.3)');
            }

            // ── 卡片激活（起点）霓虹渐变边框 ──
            const cardBorderGradActive = defs.append('linearGradient')
                .attr('id', 'card-border-grad-active')
                .attr('x1', '0%').attr('y1', '0%')
                .attr('x2', '100%').attr('y2', '100%');
            if (document.body.classList.contains('theme-light')) {
                cardBorderGradActive.append('stop').attr('offset', '0%').attr('stop-color', '#818cf8');
                cardBorderGradActive.append('stop').attr('offset', '100%').attr('stop-color', '#6366f1');
            } else {
                cardBorderGradActive.append('stop').attr('offset', '0%').attr('stop-color', '#fb7185');
                cardBorderGradActive.append('stop').attr('offset', '100%').attr('stop-color', '#f59e0b');
            }

            // 发光滤镜
            const glow = defs.append('filter').attr('id', 'card-glow');
            glow.append('feGaussianBlur').attr('stdDeviation', '4').attr('result', 'blur');
            glow.append('feComposite').attr('in', 'SourceGraphic').attr('in2', 'blur').attr('operator', 'over');

            // 箭头标记（白色小圆点）
            defs.append('marker')
                .attr('id', 'circle-end')
                .attr('viewBox', '0 0 10 10')
                .attr('refX', 5).attr('refY', 5)
                .attr('markerWidth', 6).attr('markerHeight', 6)
                .attr('orient', 'auto')
                .append('circle')
                .attr('cx', 5).attr('cy', 5).attr('r', 3)
                .attr('fill', THEME.linkColor);

            defs.append('marker')
                .attr('id', 'circle-end-hl')
                .attr('viewBox', '0 0 10 10')
                .attr('refX', 5).attr('refY', 5)
                .attr('markerWidth', 6).attr('markerHeight', 6)
                .attr('orient', 'auto')
                .append('circle')
                .attr('cx', 5).attr('cy', 5).attr('r', 3)
                .attr('fill', THEME.linkHighlight);

            this.g = this.svg.append('g');

            // ── 在 zoom 容器最底层渲染网格背景 ──
            this.g.append('rect')
                .attr('x', -50000)
                .attr('y', -50000)
                .attr('width', 100000)
                .attr('height', 100000)
                .attr('fill', 'url(#tech-grid)')
                .style('pointer-events', 'none');

            if (!this.zoom) {
                this.zoom = d3.zoom()
                    .scaleExtent([0.05, 4])
                    .on('zoom', (event) => {
                        this.g.attr('transform', event.transform);
                    });
                this.svg.call(this.zoom);
            }
        }

        bindEvents() {
            this.canvasContainer.addEventListener('contextmenu', (event) => {
                if (event.target.closest('.explore-card')) return;
                event.preventDefault();
                if (window.SourceAstra && window.SourceAstra.showContextMenu) {
                    window.SourceAstra.showContextMenu(event.clientX, event.clientY, '__project__', '整个项目', {
                        docTargets: [{ docType: 'project', docKey: '__project__', autoGenerate: true }]
                    });
                }
            });

            let _resizeTimer = 0;
            window.addEventListener('resize', () => {
                clearTimeout(_resizeTimer);
                _resizeTimer = setTimeout(() => {
                    if (this.container) {
                        const rect = this.container.getBoundingClientRect();
                        this.width = Math.max(100, rect.width);
                        this.height = Math.max(100, rect.height);
                        if (this.svg) {
                            this.svg.attr('viewBox', `0 0 ${this.width} ${this.height}`);
                        }
                    }
                }, 150);
            });
        }

        async load() {
            if (this.loading) return;
            // 如果已有缓存数据，直接重绘（切回视图时）
            if (this.rawData) {
                // 强制重建 SVG（切走再切回来时尺寸可能变化）
                this.width = 0;
                this.height = 0;
                this.svg = null;
                this.canvasContainer.innerHTML = '';
                this.updateSize();
                if (this.currentLevel === 'trace' && this.currentFuncId) {
                    this._drawTrace(this.currentFuncId);
                } else if (this.currentLevel === 'file' && this.currentFile) {
                    this._drawFile(this.currentFile);
                } else if (this.currentLevel === 'module' && this.currentDir) {
                    this._drawModule(this.currentDir);
                } else {
                    this._drawOverview();
                }
                return;
            }

            const isProjected = window.G_GRAPH_MODE === 'projected';
            if (!isProjected && (!window.G_DATA || !window.G_DATA.symbols)) {
                this.canvasContainer.innerHTML = `<div style="color:${THEME.textDim};padding:80px;text-align:center;font-size:14px;">Waiting for data...</div>`;
                return;
            }
            if (isProjected && !window.G_PROJECTED_GRAPH) {
                this.canvasContainer.innerHTML = `<div style="color:${THEME.textDim};padding:80px;text-align:center;font-size:14px;">Waiting for projected overview...</div>`;
                return;
            }

            this.loading = true;
            this.canvasContainer.innerHTML = `<div style="color:${THEME.textDim};padding:80px;text-align:center;font-size:14px;">Loading architecture...</div>`;

            try {
                if (isProjected) {
                    this.rawData = {
                        symbols: [],
                        edges: [],
                        nodes: [],
                        links: []
                    };
                } else {
                    const syms = Object.values(window.G_DATA.symbols);
                    const edges = window.G_DATA.edges || [];
                    this.rawData = {
                        symbols: syms,
                        edges: edges,
                        nodes: syms,
                        links: edges
                    };
                }

                this.canvasContainer.innerHTML = '';
                this.svg = null;
                this.updateSize();
                this._buildAggregation();
                this._drawOverview();
            } catch (err) {
                console.error('[Explore] Load failed:', err);
                this.canvasContainer.innerHTML = `
                    <div style="color:#c97070;padding:80px;text-align:center;">
                        <div style="font-size:16px;margin-bottom:8px;">Load Failed</div>
                        <div style="font-size:12px;color:${THEME.textDim};">${err.message}</div>
                    </div>`;
            } finally {
                this.loading = false;
            }
        }

        // ── 构建目录聚合 ──
        _buildAggregation() {
            if (window.G_GRAPH_MODE === 'projected' && window.G_PROJECTED_GRAPH) {
                this.dirMap = {};
                this.fileMap = {};
                
                (window.G_PROJECTED_GRAPH.nodes || []).forEach(n => {
                    const dir = n.id;
                    const dirName = n.name || dir;
                    this.dirMap[dir] = {
                        id: 'dir:' + dir,
                        name: dirName,
                        dirPath: dir,
                        files: {},
                        functions: [],
                        inDegree: 0,
                        outDegree: 0,
                        level: 3,
                        loaded: false,
                    };
                });

                const edgeMap = {};
                (window.G_PROJECTED_GRAPH.links || []).forEach(l => {
                    const key = l.source + '→' + l.target;
                    edgeMap[key] = { source: 'dir:' + l.source, target: 'dir:' + l.target, count: l.weight || 1 };
                });
                this.dirEdges = Object.values(edgeMap);
                this.fileEdges = [];

                this.dirEdges.forEach(e => {
                    const src = this.dirMap[e.source.replace('dir:', '')];
                    const tgt = this.dirMap[e.target.replace('dir:', '')];
                    if (src) src.outDegree += e.count;
                    if (tgt) tgt.inDegree += e.count;
                });
                return;
            }

            // 兼容两种数据格式：/api/data 返回 {symbols, edges}，/api/hierarchy 返回 {nodes, links}
            // 同时过滤声明/接口文件中的孤立节点，保持探索视图与源码星图口径一致。
            const rawNodes = (this.rawData.symbols || this.rawData.nodes || []).filter(n => {
                return !n.file || !window.isSourceDeclarationFile || !window.isSourceDeclarationFile(n.file);
            });
            const rawLinks = this.rawData.edges || this.rawData.links || [];

            this.allNodesMap = {};
            rawNodes.forEach(n => { this.allNodesMap[n.id] = n; });

            this.dirMap = {};
            this.fileMap = {};



            rawNodes.forEach(n => {
                const file = n.file || '(unknown file)';
                const physicalDir = this._extractDir(file);
                const dir = physicalDir !== '(root)' ? physicalDir : '(root)';
                const dirName = this._dirDisplayName(dir);

                if (!this.dirMap[dir]) {
                    this.dirMap[dir] = {
                        id: 'dir:' + dir,
                        name: dirName,
                        dirPath: dir,
                        files: {},
                        functions: [],
                        inDegree: 0,
                        outDegree: 0,
                        level: 3,
                    };
                }
                this.dirMap[dir].functions.push(n);

                if (!this.dirMap[dir].files[file]) {
                    this.dirMap[dir].files[file] = {
                        id: 'file:' + file,
                        name: file.split('/').pop(),
                        filePath: file,
                        dirPath: dir,
                        functions: [],
                        inDegree: 0,
                        outDegree: 0,
                    };
                }
                this.dirMap[dir].files[file].functions.push(n);
                this.fileMap[file] = this.dirMap[dir].files[file];
            });

            const edgeMap = {};
            const fileEdgeMap = {};
            rawLinks.forEach(l => {
                const srcNode = this.allNodesMap[l.from];
                const tgtNode = this.allNodesMap[l.to];
                if (!srcNode || !tgtNode) return;

                if (window.isSourceDeclarationFile && (window.isSourceDeclarationFile(srcNode.file) || window.isSourceDeclarationFile(tgtNode.file))) return;

                const srcPhysicalDir = this._extractDir(srcNode.file);
                const tgtPhysicalDir = this._extractDir(tgtNode.file);
                const srcDir = srcPhysicalDir !== '(root)' ? srcPhysicalDir : '(root)';
                const tgtDir = tgtPhysicalDir !== '(root)' ? tgtPhysicalDir : '(root)';

                const srcFile = srcNode.file || '(unknown file)';
                const tgtFile = tgtNode.file || '(unknown file)';

                if (srcDir !== tgtDir) {
                    const key = srcDir + '→' + tgtDir;
                    if (!edgeMap[key]) {
                        edgeMap[key] = { source: 'dir:' + srcDir, target: 'dir:' + tgtDir, count: 0 };
                    }
                    edgeMap[key].count++;
                }

                if (srcFile !== tgtFile) {
                    const fKey = srcFile + '→' + tgtFile;
                    if (!fileEdgeMap[fKey]) {
                        fileEdgeMap[fKey] = { source: 'file:' + srcFile, target: 'file:' + tgtFile, count: 0 };
                    }
                    fileEdgeMap[fKey].count++;
                }
            });
            this.dirEdges = Object.values(edgeMap);
            this.fileEdges = Object.values(fileEdgeMap);

            this.dirEdges.forEach(e => {
                const src = this.dirMap[e.source.replace('dir:', '')];
                const tgt = this.dirMap[e.target.replace('dir:', '')];
                if (src) src.outDegree += e.count;
                if (tgt) tgt.inDegree += e.count;
            });

            this.fileEdges.forEach(e => {
                const src = this.fileMap[e.source.replace('file:', '')];
                const tgt = this.fileMap[e.target.replace('file:', '')];
                if (src) src.outDegree += e.count;
                if (tgt) tgt.inDegree += e.count;
            });
        }

        _extractDir(filePath) {
            if (!filePath) return '(root)';
            const parts = filePath.replace(/\\/g, '/').split('/');
            if (parts.length <= 1) return '(root)';
            return parts[0] || '(root)';
        }

        _dirDisplayName(dirPath) {
            if (dirPath === '(root)') return '(root)';
            return dirPath;
        }

        _truncate(str, maxLen) {
            if (!str) return '';
            return str.length > maxLen ? str.slice(0, maxLen - 1) + '…' : str;
        }

        // ── 概览层：目录聚合大卡片 ──
        _drawOverview() {
            this.currentLevel = 'top';
            this.currentDir = null;
            window.G_SELECTED = null;
            this.currentFile = null;
            this.currentFuncId = null;
            this.breadcrumb = [{ label: 'Top', action: () => this._drawOverview() }];
            this._renderBreadcrumb();

            if (this.simulation) { this.simulation.stop(); this.simulation = null; }
            this.g.selectAll('*').remove();

            const dirs = Object.values(this.dirMap);
            if (dirs.length === 0) {
                this.canvasContainer.innerHTML = `<div style="color:${THEME.textDim};padding:80px;text-align:center;">No data</div>`;
                return;
            }

            if (this.statsEl) {
                this.statsEl.textContent = `${dirs.length} directories · ${this.dirEdges.length} connections`;
            }

            const nodes = dirs.map(d => {
                const funcCount = d.functions.length;
                const topFuncs = d.functions.slice(0, 3).map(f => f.name || f.id);
                const cardH = CARD.headerHeight + CARD.descLineHeight + topFuncs.length * CARD.entityLineHeight + CARD.tagHeight + CARD.padding * 2 + 16;
                return {
                    id: d.id,
                    name: d.name,
                    dirPath: d.dirPath,
                    funcCount: funcCount,
                    topFuncs: topFuncs,
                    inDeg: d.inDegree,
                    outDeg: d.outDegree,
                    totalDeg: d.inDegree + d.outDegree,
                    cardWidth: CARD.width,
                    cardHeight: cardH,
                    level: d.level,
                };
            });

            const links = this.dirEdges.map(e => ({
                source: e.source,
                target: e.target,
                count: e.count,
            }));

            this.nodes = nodes;
            this.links = links;

            const nodeCount = nodes.length;
            const chargeStr = nodeCount > 20 ? -1000 : nodeCount > 10 ? -800 : -500;
            const linkDist = nodeCount > 20 ? 400 : nodeCount > 10 ? 350 : 280;
            const collDist = nodeCount > 20 ? 40 : 30;

            this.simulation = d3.forceSimulation(nodes)
                .force('link', d3.forceLink(links).id(d => d.id).distance(linkDist))
                .force('charge', d3.forceManyBody().strength(chargeStr))
                .force('center', d3.forceCenter(this.width / 2, this.height / 2))
                .force('collision', d3.forceCollide().radius(d => Math.max(d.cardWidth, d.cardHeight) / 2 + collDist))
                .force('x', d3.forceX(this.width / 2).strength(0.04))
                .force('y', d3.forceY(d => {
                    const lvl = d.level != null ? d.level : 3;
                    // 层级越高 (level 5/6) 约束在上方 (Y 越小)，层级越低在下方
                    return this.height * (0.85 - (lvl / 6.0) * 0.7);
                }).strength(0.18))
                .alphaDecay(0.02);

            this._renderCards(nodes, links, true);
        }

        async _drawModule(dirPath) {
            this.currentLevel = 'module';
            this.currentDir = dirPath;
            window.G_SELECTED = null;
            this.currentFile = null;
            this.currentFuncId = null;
            this.breadcrumb = [
                { label: 'Top', action: () => this._drawOverview() },
                { label: this._dirDisplayName(dirPath), action: () => this._drawModule(dirPath) }
            ];
            this._renderBreadcrumb();

            if (this.simulation) { this.simulation.stop(); this.simulation = null; }
            this.g.selectAll('*').remove();

            const dirData = this.dirMap ? this.dirMap[dirPath] : null;
            console.log('[Explore] _drawModule: dirPath =', dirPath, 'dirData =', dirData);
            if (!dirData) return;

            // 大项目投影模式下动态加载模块详情
            if (window.G_GRAPH_MODE === 'projected' && !dirData.loaded) {
                if (this.statsEl) {
                    this.statsEl.textContent = "Loading module details...";
                }
                try {
                    const res = await fetch(`/api/graph/module?id=${encodeURIComponent(dirPath)}`, {
                        headers: window.saAuthHeaders()
                    });
                    if (!res.ok) throw new Error('HTTP ' + res.status);
                    const subgraph = await res.json();
                    
                    // 填充模块内部的 files 和 functions
                    dirData.files = {};
                    (subgraph.nodes || []).forEach(n => {
                        const file = n.file || '(unknown file)';
                        if (!dirData.files[file]) {
                            dirData.files[file] = {
                                id: 'file:' + file,
                                name: file.split('/').pop(),
                                filePath: file,
                                dirPath: dirPath,
                                functions: [],
                                inDegree: 0,
                                outDegree: 0,
                            };
                            this.fileMap[file] = dirData.files[file];
                        }
                        dirData.files[file].functions.push(n);
                    });

                    // 同步到全局 window.G_DATA.symbols 以打通数据并动态更新左侧函数树
                    if (window.G_DATA && window.G_DATA.symbols) {
                        (subgraph.nodes || []).forEach(n => {
                            if (!window.G_DATA.symbols[n.id]) {
                                window.G_DATA.symbols[n.id] = {
                                    id: n.id,
                                    name: n.name,
                                    type: n.type,
                                    file: n.file,
                                    line: n.line,
                                    module_id: n.module_id
                                };
                            }
                        });
                        if (window.G_CURRENT_VIEW === 'explore' || window.G_CURRENT_VIEW === 'nebula' || window.G_CURRENT_VIEW === 'trace') {
                            this._renderBreadcrumb();
                        } else if (typeof window.buildTree === 'function') {
                            window.buildTree();
                        }
                    }

                    // 同步到 explore view 本地的 rawData 以便 _drawTrace 和子视图绘制
                    if (this.rawData) {
                        (subgraph.nodes || []).forEach(n => {
                            if (!this.rawData.nodes.some(x => x.id === n.id)) {
                                this.rawData.nodes.push(n);
                            }
                        });
                        (subgraph.edges || []).forEach(e => {
                            if (!this.rawData.links.some(x => x.from === e.from && x.to === e.to)) {
                                this.rawData.links.push(e);
                            }
                        });
                    }

                    // 填充模块内部边
                    (subgraph.edges || []).forEach(e => {
                        const srcNode = subgraph.nodes.find(n => n.id === e.from);
                        const tgtNode = subgraph.nodes.find(n => n.id === e.to);
                        if (!srcNode || !tgtNode) return;
                        const srcFile = srcNode.file || '(unknown file)';
                        const tgtFile = tgtNode.file || '(unknown file)';
                        
                        if (srcFile !== tgtFile) {
                            // 检查 this.fileEdges 是否已有这条边
                            if (!this.fileEdges.some(x => x.source === 'file:' + srcFile && x.target === 'file:' + tgtFile)) {
                                this.fileEdges.push({ source: 'file:' + srcFile, target: 'file:' + tgtFile, count: 1 });
                            }
                        }
                    });
                    
                    // 计算出入度
                    this.fileEdges.forEach(e => {
                        const src = this.fileMap[e.source.replace('file:', '')];
                        const tgt = this.fileMap[e.target.replace('file:', '')];
                        if (src && src.dirPath === dirPath) src.outDegree++;
                        if (tgt && tgt.dirPath === dirPath) tgt.inDegree++;
                    });

                    dirData.loaded = true;

                } catch (e) {
                    console.error('[Explore] Fetch module subgraph failed:', e);
                    if (this.statsEl) {
                        this.statsEl.textContent = "Load failed: " + e.message;
                    }
                    return;
                }
            }

            const files = Object.values(dirData.files);
            if (files.length === 0) return;

            const fileEdges = this.fileEdges.filter(e => {
                const s = this.fileMap[e.source.replace('file:', '')];
                const t = this.fileMap[e.target.replace('file:', '')];
                return s && t && s.dirPath === dirPath && t.dirPath === dirPath;
            });

            if (this.statsEl) {
                this.statsEl.textContent = `${files.length} files · ${fileEdges.length} connections`;
            }

            const nodes = files.map(f => {
                const funcCount = f.functions.length;
                const topFuncs = f.functions.slice(0, 3).map(fn => fn.name || fn.id);
                const cardH = CARD.headerHeight + CARD.descLineHeight + topFuncs.length * CARD.entityLineHeight + CARD.tagHeight + CARD.padding * 2 + 16;
                return {
                    id: f.id,
                    name: f.name,
                    filePath: f.filePath,
                    funcCount: funcCount,
                    topFuncs: topFuncs,
                    inDeg: f.inDegree,
                    outDeg: f.outDegree,
                    totalDeg: f.inDegree + f.outDegree,
                    cardWidth: CARD.width,
                    cardHeight: cardH,
                };
            });

            const links = fileEdges.map(e => ({
                source: e.source,
                target: e.target,
                count: e.count,
            }));

            this.nodes = nodes;
            this.links = links;

            const nodeCount = nodes.length;
            const chargeStr = nodeCount > 20 ? -1000 : nodeCount > 10 ? -800 : -500;
            const linkDist = nodeCount > 20 ? 400 : nodeCount > 10 ? 350 : 280;
            const collDist = nodeCount > 20 ? 40 : 30;

            this.simulation = d3.forceSimulation(nodes)
                .force('link', d3.forceLink(links).id(d => d.id).distance(linkDist))
                .force('charge', d3.forceManyBody().strength(chargeStr))
                .force('center', d3.forceCenter(this.width / 2, this.height / 2))
                .force('collision', d3.forceCollide().radius(d => Math.max(d.cardWidth, d.cardHeight) / 2 + collDist))
                .force('x', d3.forceX(this.width / 2).strength(0.04))
                .force('y', d3.forceY(this.height / 2).strength(0.04))
                .alphaDecay(0.02);

            this._renderCards(nodes, links, true);
        }

        _drawFile(filePath, isRetry = false) {
            this.currentLevel = 'file';
            this.currentFile = filePath;
            window.G_SELECTED = null;
            this.currentDir = (this.fileMap && this.fileMap[filePath]) ? this.fileMap[filePath].dirPath : null;
            this.currentFuncId = null;
            
            const dirName = this.currentDir ? this._dirDisplayName(this.currentDir) : 'Unknown';
            const fileName = filePath.split('/').pop();
            
            this.breadcrumb = [
                { label: 'Top', action: () => this._drawOverview() },
                { label: dirName, action: () => this._drawModule(this.currentDir) },
                { label: fileName, action: () => this._drawFile(filePath) }
            ];
            this._renderBreadcrumb();

            if (this.simulation) { this.simulation.stop(); this.simulation = null; }
            this.g.selectAll('*').remove();

            const fileData = this.fileMap ? this.fileMap[filePath] : null;
            if (!fileData) return;

            // 大仓库投影模式下按需动态拉取模块子图数据
            if (window.G_GRAPH_MODE === 'projected' && (!fileData.functions || fileData.functions.length === 0)) {
                if (isRetry) {
                    if (this.statsEl) {
                        this.statsEl.textContent = "No functions found in this file.";
                    }
                    return;
                }
                if (this.statsEl) {
                    this.statsEl.textContent = "Loading module files data...";
                }
                this._drawModule(this.currentDir).then(() => {
                    this._drawFile(filePath, true);
                });
                return;
            }

            const funcNodes = fileData.functions;
            const nodeMap = {};
            funcNodes.forEach(f => {
                const cardH = CARD.headerHeight + CARD.descLineHeight + CARD.tagHeight + CARD.padding * 2 + 8;
                nodeMap[f.id] = {
                    id: f.id,
                    name: f.name || f.id,
                    type: f.type || 'function',
                    file: f.file || '',
                    fileShort: fileName,
                    line: f.line || 0,
                    cardWidth: CARD.width,
                    cardHeight: cardH,
                    depth: -1,
                    children: [],
                    parents: [],
                    x: 0,
                    y: 0,
                };
            });

            const nodeIds = new Set(Object.keys(nodeMap));
            const links = [];
            (this.rawData.links || []).forEach(l => {
                if (nodeIds.has(l.from) && nodeIds.has(l.to) && l.from !== l.to) {
                    links.push({ source: l.from, target: l.to, count: 1 });
                    if (nodeMap[l.from] && nodeMap[l.to]) {
                        nodeMap[l.from].children.push(l.to);
                        nodeMap[l.to].parents.push(l.from);
                    }
                }
            });

            // DAG Layout Logic
            const entryNodes = Object.values(nodeMap).filter(n => n.parents.length === 0);
            let roots = entryNodes.length > 0 ? entryNodes : 
                Object.values(nodeMap).sort((a, b) => b.children.length - a.children.length).slice(0, 3);

            const visited = new Set();
            const queue = [];
            roots.forEach(r => {
                r.depth = 0;
                visited.add(r.id);
                queue.push(r);
            });

            while (queue.length > 0) {
                const node = queue.shift();
                node.children.forEach(childId => {
                    if (!nodeMap[childId]) return;
                    const child = nodeMap[childId];
                    const newDepth = node.depth + 1;
                    if (!visited.has(childId) || child.depth < newDepth) {
                        child.depth = Math.max(child.depth, newDepth);
                        if (!visited.has(childId)) {
                            visited.add(childId);
                            queue.push(child);
                        }
                    }
                });
            }

            let maxDepth = 0;
            Object.values(nodeMap).forEach(n => {
                if (n.depth >= 0) maxDepth = Math.max(maxDepth, n.depth);
            });
            Object.values(nodeMap).forEach(n => {
                if (n.depth < 0) {
                    maxDepth++;
                    n.depth = maxDepth;
                }
            });

            const layers = {};
            Object.values(nodeMap).forEach(n => {
                const d = n.depth;
                if (!layers[d]) layers[d] = [];
                layers[d].push(n);
            });

            Object.values(layers).forEach(layer => {
                layer.sort((a, b) => b.children.length - a.children.length);
            });

            const layerKeys = Object.keys(layers).map(Number).sort((a, b) => a - b);
            const layerCount = layerKeys.length;
            const maxNodesInLayer = Math.max(...Object.values(layers).map(l => l.length));
            const viewW = this.width;
            const viewH = this.height;
            
            const maxPerRow = Math.max(1, Math.floor((viewW - 80) / (CARD.width + 20)));
            const nodeSpacingX = Math.min(CARD.width + 40, (viewW - 80) / Math.min(maxPerRow, maxNodesInLayer));
            const nodeSpacingY = 160;
            const rowSpacingY = 100;

            let totalHeight = 0;
            const layerLayouts = [];
            layerKeys.forEach(depth => {
                const nodesInLayer = layers[depth];
                const rows = Math.ceil(nodesInLayer.length / maxPerRow);
                layerLayouts.push({ depth, nodes: nodesInLayer, rows });
                totalHeight += (rows - 1) * rowSpacingY + nodeSpacingY;
            });

            const startY = Math.max(40, (viewH - totalHeight) / 2);

            let currentY = startY;
            layerKeys.forEach((depth, layerIdx) => {
                const layout = layerLayouts[layerIdx];
                const nodesInLayer = layout.nodes;
                const rows = layout.rows;

                for (let row = 0; row < rows; row++) {
                    const startIdx = row * maxPerRow;
                    const endIdx = Math.min(startIdx + maxPerRow, nodesInLayer.length);
                    const rowNodes = nodesInLayer.slice(startIdx, endIdx);
                    const rowWidth = rowNodes.length * nodeSpacingX;
                    const startX = Math.max(20, (viewW - rowWidth) / 2);

                    rowNodes.forEach((node, nodeIdx) => {
                        node.x = startX + nodeIdx * nodeSpacingX;
                        node.y = currentY + row * rowSpacingY;
                    });
                }

                currentY += (rows - 1) * rowSpacingY + nodeSpacingY;
            });

            const nodes = Object.values(nodeMap);

            if (this.statsEl) {
                this.statsEl.textContent = `${nodes.length} functions · ${links.length} calls · ${layerCount} layers`;
            }

            this.nodes = nodes;
            this.links = links;

            this._renderChainView(nodes, links);
        }

        _drawTrace(funcId, isRetry = false) {
            this.currentLevel = 'trace';
            this.currentFuncId = funcId;
            if (window.G_DATA && window.G_DATA.symbols[funcId]) {
                window.G_SELECTED = window.G_DATA.symbols[funcId];
            }

            const funcNode = (this.rawData.nodes || []).find(n => n.id === funcId);
            let filePath = funcNode ? (funcNode.file || '') : '';
            if (!filePath && funcId.includes('::')) {
                filePath = funcId.split('::')[0];
            }

            // 大仓库投影模式下按需动态拉取模块子图数据
            if (window.G_GRAPH_MODE === 'projected' && (!funcNode || !this.fileMap[filePath] || !this.fileMap[filePath].functions || this.fileMap[filePath].functions.length === 0)) {
                if (isRetry) {
                    if (this.statsEl) {
                        this.statsEl.textContent = "Trace target node not found.";
                    }
                    return;
                }
                const fMapEntry = this.fileMap[filePath];
                const dir = fMapEntry ? fMapEntry.dirPath : (window.G_FILE_TO_MODULE && window.G_FILE_TO_MODULE[filePath] ? window.G_FILE_TO_MODULE[filePath].id : null);
                if (dir) {
                    if (this.statsEl) {
                        this.statsEl.textContent = "Loading trace data...";
                    }
                    this._drawModule(dir).then(() => {
                        this._drawTrace(funcId, true);
                    });
                    return;
                }
            }

            this.currentFile = filePath;
            this.currentDir = this.fileMap[filePath] ? this.fileMap[filePath].dirPath : null;

            const dirName = this.currentDir ? this._dirDisplayName(this.currentDir) : 'Unknown';
            const fileName = filePath.split('/').pop() || 'Unknown';
            const funcName = funcNode ? (funcNode.name || funcId) : funcId;

            this.breadcrumb = [
                { label: 'Top', action: () => this._drawOverview() },
                { label: dirName, action: () => this._drawModule(this.currentDir) },
                { label: fileName, action: () => this._drawFile(filePath) },
                { label: this._truncate(funcName, 20), action: () => this._drawTrace(funcId) }
            ];
            this._renderBreadcrumb();

            if (this.simulation) { this.simulation.stop(); this.simulation = null; }
            this.g.selectAll('*').remove();

            const MAX_DEPTH = 5;
            const nodeMap = {};
            const linkSet = new Set();
            const links = [];

            const allNodes = this.rawData.nodes || [];
            const allLinks = this.rawData.links || [];
            const nodeById = {};
            allNodes.forEach(n => { nodeById[n.id] = n; });

            // 预构建邻接表，避免 BFS 中 O(depth*E) 的全量扫描
            const outgoing = {}; // id -> [calleeId, ...]
            const incoming = {}; // id -> [callerId, ...]
            allLinks.forEach(l => {
                if (l.from === l.to) return;
                (outgoing[l.from] || (outgoing[l.from] = [])).push(l.to);
                (incoming[l.to] || (incoming[l.to] = [])).push(l.from);
            });

            const startNode = nodeById[funcId];
            if (!startNode) return;

            nodeMap[funcId] = {
                id: funcId,
                name: startNode.name || funcId,
                type: startNode.type || 'function',
                file: startNode.file || '',
                fileShort: (startNode.file || '').split('/').pop(),
                line: startNode.line || 0,
                cardWidth: CARD.width,
                cardHeight: CARD.headerHeight + CARD.descLineHeight + CARD.tagHeight + CARD.padding * 2 + 8,
                depth: 0,
                isRoot: true,
                x: 0, y: 0,
            };

            const visitedDown = new Set([funcId]);
            const visitedUp = new Set([funcId]);
            const queueDown = [{ id: funcId, depth: 0 }];
            const queueUp = [{ id: funcId, depth: 0 }];
            const calleeMap = {};
            const callerMap = {};

            while (queueDown.length > 0) {
                const { id, depth } = queueDown.shift();
                if (depth >= MAX_DEPTH) continue;
                const targets = outgoing[id] || [];
                targets.forEach(toId => {
                    if (!visitedDown.has(toId)) {
                        visitedDown.add(toId);
                        const target = nodeById[toId];
                        if (!target) return;
                        const cardH = CARD.headerHeight + CARD.descLineHeight + CARD.tagHeight + CARD.padding * 2 + 8;
                        nodeMap[toId] = {
                            id: toId,
                            name: target.name || toId,
                            type: target.type || 'function',
                            file: target.file || '',
                            fileShort: (target.file || '').split('/').pop(),
                            line: target.line || 0,
                            cardWidth: CARD.width,
                            cardHeight: cardH,
                            depth: depth + 1,
                            isRoot: false,
                            direction: 'down',
                            x: 0, y: 0,
                        };
                        if (!calleeMap[id]) calleeMap[id] = [];
                        calleeMap[id].push(toId);

                        const linkKey = id + '→' + toId;
                        if (!linkSet.has(linkKey)) {
                            linkSet.add(linkKey);
                            links.push({ source: id, target: toId, count: 1 });
                        }
                        queueDown.push({ id: toId, depth: depth + 1 });
                    }
                });
            }

            while (queueUp.length > 0) {
                const { id, depth } = queueUp.shift();
                if (depth >= MAX_DEPTH) continue;
                const sources = incoming[id] || [];
                sources.forEach(fromId => {
                    if (!visitedUp.has(fromId)) {
                        visitedUp.add(fromId);
                        const source = nodeById[fromId];
                        if (!source) return;
                        const cardH = CARD.headerHeight + CARD.descLineHeight + CARD.tagHeight + CARD.padding * 2 + 8;
                        nodeMap[fromId] = {
                            id: fromId,
                            name: source.name || fromId,
                            type: source.type || 'function',
                            file: source.file || '',
                            fileShort: (source.file || '').split('/').pop(),
                            line: source.line || 0,
                            cardWidth: CARD.width,
                            cardHeight: cardH,
                            depth: -(depth + 1),
                            isRoot: false,
                            direction: 'up',
                            x: 0, y: 0,
                        };
                        if (!callerMap[id]) callerMap[id] = [];
                        callerMap[id].push(fromId);

                        const linkKey = fromId + '→' + id;
                        if (!linkSet.has(linkKey)) {
                            linkSet.add(linkKey);
                            links.push({ source: fromId, target: id, count: 1 });
                        }
                        queueUp.push({ id: fromId, depth: depth + 1 });
                    }
                });
            }

            // ===== 双向发散树形布局定位算法 =====
            const viewW = this.width || 1000;
            const viewH = this.height || 800;
            const nodeSpacingX = CARD.width + 50; // 270px 横向基准间距
            const nodeSpacingY = 160;            // 纵向层高

            // 根节点定位在画布中心
            const rootNode = nodeMap[funcId];
            if (rootNode) {
                rootNode.x = (viewW - rootNode.cardWidth) / 2;
                rootNode.y = (viewH - rootNode.cardHeight) / 2;
            }

            // 向下分配（Callees）
            const assignDown = (parentId) => {
                const children = calleeMap[parentId] || [];
                if (children.length === 0) return;
                const pNode = nodeMap[parentId];
                if (!pNode) return;
                const cCount = children.length;
                children.forEach((cId, idx) => {
                    const cNode = nodeMap[cId];
                    if (cNode) {
                        cNode.y = pNode.y + nodeSpacingY;
                        cNode.x = pNode.x + (idx - (cCount - 1) / 2) * nodeSpacingX;
                        assignDown(cId);
                    }
                });
            };
            assignDown(funcId);

            // 向上分配（Callers）
            const assignUp = (childId) => {
                const parents = callerMap[childId] || [];
                if (parents.length === 0) return;
                const cNode = nodeMap[childId];
                if (!cNode) return;
                const pCount = parents.length;
                parents.forEach((pId, idx) => {
                    const pNode = nodeMap[pId];
                    if (pNode) {
                        pNode.y = cNode.y - nodeSpacingY;
                        pNode.x = cNode.x + (idx - (pCount - 1) / 2) * nodeSpacingX;
                        assignUp(pId);
                    }
                });
            };
            assignUp(funcId);

            // 局部弹簧避让算法，防止节点横向重合
            const layersY = {};
            Object.values(nodeMap).forEach(node => {
                const layerIdx = Math.round(node.y / nodeSpacingY);
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
                        const minGap = CARD.width + 30; // 横向卡片之间的最小间隙安全线
                        const overlap = (curr.x + minGap) - next.x;
                        if (overlap > 0) {
                            curr.x -= overlap / 2;
                            next.x += overlap / 2;
                        }
                    }
                }
            });

            // 整体居中校准器（回正算法）
            const nodesArr = Object.values(nodeMap);
            if (nodesArr.length > 0) {
                let minX = Infinity, maxX = -Infinity;
                let minY = Infinity, maxY = -Infinity;
                nodesArr.forEach(n => {
                    if (n.x < minX) minX = n.x;
                    if (n.x > maxX) maxX = n.x;
                    if (n.y < minY) minY = n.y;
                    if (n.y > maxY) maxY = n.y;
                });

                const graphW = (maxX - minX) + CARD.width;
                const graphH = (maxY - minY) + (rootNode ? rootNode.cardHeight : 100);
                const dx = (viewW - graphW) / 2 - minX;
                const dy = (viewH - graphH) / 2 - minY;

                nodesArr.forEach(n => {
                    n.x += dx;
                    n.y += dy;
                });
            }

            const layerCount = Object.keys(layersY).length;

            const nodes = Object.values(nodeMap);

            if (this.statsEl) {
                this.statsEl.textContent = `${nodes.length} functions · ${links.length} calls · ${layerCount} layers`;
            }

            this.nodes = nodes;
            this.links = links;

            this._renderChainView(nodes, links);
        }

        _renderChainView(nodes, links) {
            const nodeMap = {};
            nodes.forEach(n => { nodeMap[n.id] = n; });

            // ── 绘制连线（曲线，从父节点底部到子节点顶部）──
            this.linkElements = this.g.append('g')
                .selectAll('g')
                .data(links)
                .enter().append('g');

            this.linkElements.append('path')
                .attr('class', 'explore-link')
                .attr('fill', 'none')
                .attr('stroke', THEME.linkColor)
                .attr('stroke-width', 1.5)
                .attr('stroke-dasharray', '8 4')
                .attr('marker-end', 'url(#circle-end)')
                .style('opacity', 0.7)
                .attr('d', d => {
                    const src = nodeMap[typeof d.source === 'object' ? d.source.id : d.source];
                    const tgt = nodeMap[typeof d.target === 'object' ? d.target.id : d.target];
                    if (!src || !tgt) return '';
                    // 从源节点底部中心 → 目标节点顶部中心
                    const x1 = src.x + src.cardWidth / 2;
                    const y1 = src.y + src.cardHeight;
                    const x2 = tgt.x + tgt.cardWidth / 2;
                    const y2 = tgt.y;
                    // 贝塞尔曲线：垂直方向的控制点
                    const midY = (y1 + y2) / 2;
                    return `M${x1},${y1} C${x1},${midY} ${x2},${midY} ${x2},${y2}`;
                });

            // ── 绘制节点卡片 ──
            this.nodeElements = this.g.append('g')
                .selectAll('g')
                .data(nodes)
                .enter().append('g')
                .attr('class', 'explore-card')
                .attr('transform', d => `translate(${d.x},${d.y})`)
                .style('cursor', 'pointer');

            // 卡片背景
            this.nodeElements.append('rect')
                .attr('class', 'card-bg')
                .attr('width', d => d.cardWidth)
                .attr('height', d => d.cardHeight)
                .attr('rx', CARD.radius)
                .attr('ry', CARD.radius)
                .attr('fill', d => d.id === this.currentFuncId ? 'url(#card-bg-grad-active)' : THEME.cardBg)
                .attr('stroke', d => d.id === this.currentFuncId ? 'url(#card-border-grad-active)' : THEME.cardBorder)
                .attr('stroke-width', d => d.id === this.currentFuncId ? 2 : 1);

            // 标题
            this.nodeElements.append('text')
                .attr('class', 'card-title')
                .attr('x', CARD.padding)
                .attr('y', CARD.headerHeight)
                .attr('fill', THEME.titleColor)
                .attr('font-size', '16px')
                .attr('font-weight', '800')
                .attr('font-family', 'Roboto, sans-serif')
                .text(d => this._truncate(d.name, 24));

            // 分隔线1（标题下方）
            this.nodeElements.append('line')
                .attr('x1', CARD.padding - 2)
                .attr('y1', CARD.headerHeight + 6)
                .attr('x2', d => d.cardWidth - CARD.padding + 2)
                .attr('y2', CARD.headerHeight + 6)
                .attr('stroke', THEME.separatorColor)
                .attr('stroke-width', 1);

            // 文件路径
            this.nodeElements.append('text')
                .attr('class', 'card-desc')
                .attr('x', CARD.padding)
                .attr('y', CARD.headerHeight + CARD.descLineHeight + 8)
                .attr('fill', THEME.descColor)
                .attr('font-size', '10px')
                .attr('font-family', 'monospace')
                .attr('opacity', 0.8)
                .text(d => this._truncate(d.fileShort || d.file || '', 28));

            // 分隔线2
            this.nodeElements.append('line')
                .attr('x1', CARD.padding - 2)
                .attr('y1', CARD.headerHeight + CARD.descLineHeight + 16)
                .attr('x2', d => d.cardWidth - CARD.padding + 2)
                .attr('y2', CARD.headerHeight + CARD.descLineHeight + 16)
                .attr('stroke', THEME.separatorColor2)
                .attr('stroke-width', 1);

            // 连接数标签
            const weightMap = {};
            links.forEach(l => {
                const sid = typeof l.source === 'object' ? l.source.id : l.source;
                const tid = typeof l.target === 'object' ? l.target.id : l.target;
                weightMap[sid] = (weightMap[sid] || 0) + 1;
                weightMap[tid] = (weightMap[tid] || 0) + 1;
            });

            this.nodeElements.append('rect')
                .attr('x', CARD.padding - 2)
                .attr('y', d => d.cardHeight - CARD.tagHeight - CARD.padding + 2)
                .attr('width', d => {
                    const w = weightMap[d.id] || 0;
                    const t = w + ' call' + (w !== 1 ? 's' : '');
                    return t.length * 7 + 12;
                })
                .attr('height', 18)
                .attr('rx', 9)
                .attr('fill', THEME.tagBg);

            this.nodeElements.append('text')
                .attr('x', CARD.padding + 4)
                .attr('y', d => d.cardHeight - CARD.tagHeight - CARD.padding + 14)
                .attr('fill', THEME.tagColor)
                .attr('font-size', '10px')
                .attr('font-weight', '600')
                .attr('font-family', 'Roboto, sans-serif')
                .text(d => {
                    const w = weightMap[d.id] || 0;
                    return w + ' call' + (w !== 1 ? 's' : '');
                });

            // ── 交互 ──
            this.nodeElements
                .on('mouseenter', (event, d) => {
                    // 高亮相关连线
                    this.linkElements.select('path')
                        .attr('stroke', l => {
                            const sid = typeof l.source === 'object' ? l.source.id : l.source;
                            const tid = typeof l.target === 'object' ? l.target.id : l.target;
                            return (sid === d.id || tid === d.id) ? THEME.linkHighlight : THEME.linkColor;
                        })
                        .attr('stroke-width', l => {
                            const sid = typeof l.source === 'object' ? l.source.id : l.source;
                            const tid = typeof l.target === 'object' ? l.target.id : l.target;
                            return (sid === d.id || tid === d.id) ? 2.5 : 0.5;
                        })
                        .style('opacity', l => {
                            const sid = typeof l.source === 'object' ? l.source.id : l.source;
                            const tid = typeof l.target === 'object' ? l.target.id : l.target;
                            return (sid === d.id || tid === d.id) ? 1 : 0.1;
                        })
                        .attr('marker-end', l => {
                            const sid = typeof l.source === 'object' ? l.source.id : l.source;
                            const tid = typeof l.target === 'object' ? l.target.id : l.target;
                            return (sid === d.id || tid === d.id) ? 'url(#circle-end-hl)' : 'url(#circle-end)';
                        });

                    d3.select(event.currentTarget).select('.card-bg')
                        .attr('stroke', THEME.cardBorderHover)
                        .style('filter', 'url(#card-glow)');
                })
                .on('mouseleave', (event, d) => {
                    this.linkElements.select('path')
                        .attr('stroke', THEME.linkColor)
                        .attr('stroke-width', 1.5)
                        .style('opacity', 0.7)
                        .attr('marker-end', 'url(#circle-end)');

                    d3.select(event.currentTarget).select('.card-bg')
                        .attr('stroke', THEME.cardBorder)
                        .style('filter', 'none');
                })
                .on('click', (event, d) => {
                    event.stopPropagation();
                    this._drawTrace(d.id);
                })
                .on('contextmenu', (event, d) => {
                    event.preventDefault();
                    event.stopPropagation();
                    if (window.SourceAstra && window.SourceAstra.showContextMenu) {
                        const docCtx = this._docContextForNode(d);
                        window.SourceAstra.showContextMenu(event.clientX, event.clientY, d.id, d.name, docCtx);
                    }
                });

            // 缩放适应
            requestAnimationFrame(() => this._fitToNodes(nodes));
        }

        _getNodeDegree(nodeId) {
            let deg = 0;
            (this.rawData.links || []).forEach(l => {
                if (l.from === nodeId || l.to === nodeId) deg++;
            });
            return deg;
        }

        // ── 通用卡片渲染 ──
        _renderCards(nodes, links, isOverview) {
            // 绘制连线（金色虚线 + 边标签）
            this.linkElements = this.g.append('g')
                .selectAll('g')
                .data(links)
                .enter().append('g');

            this.linkElements.append('line')
                .attr('class', 'explore-link')
                .attr('stroke', THEME.linkColor)
                .attr('stroke-width', d => Math.max(1.5, Math.min(3, Math.sqrt(d.count || 1) * 1.5)))
                .attr('stroke-dasharray', '8 4')
                .attr('marker-end', 'url(#circle-end)')
                .style('opacity', 0.7);

            // 边标签（"N calls"）
            this.labelElements = this.linkElements.append('text')
                .attr('class', 'edge-label')
                .attr('fill', THEME.tagColor)
                .attr('font-size', '10px')
                .attr('font-family', 'Roboto, sans-serif')
                .attr('text-anchor', 'middle')
                .attr('dy', -6)
                .text(d => {
                    const c = d.count || 1;
                    return c === 1 ? '1 call' : c + ' calls';
                })
                .style('opacity', 0.8);

            // 绘制节点卡片
            this.nodeElements = this.g.append('g')
                .selectAll('g')
                .data(nodes)
                .enter().append('g')
                .attr('class', 'explore-card')
                .style('cursor', isOverview ? 'pointer' : 'default')
                .call(d3.drag()
                    .on('start', (event, d) => {
                        if (!event.active) this.simulation.alphaTarget(0.3).restart();
                        d.fx = d.x; d.fy = d.y;
                    })
                    .on('drag', (event, d) => { d.fx = event.x; d.fy = event.y; })
                    .on('end', (event, d) => {
                        if (!event.active) this.simulation.alphaTarget(0);
                        d.fx = null; d.fy = null;
                    }));

            // 卡片背景（圆角矩形）
            this.nodeElements.append('rect')
                .attr('class', 'card-bg')
                .attr('width', d => d.cardWidth)
                .attr('height', d => d.cardHeight)
                .attr('rx', CARD.radius)
                .attr('ry', CARD.radius)
                .attr('fill', d => d.id === this.currentFuncId ? 'url(#card-bg-grad-active)' : THEME.cardBg)
                .attr('stroke', d => d.id === this.currentFuncId ? 'url(#card-border-grad-active)' : THEME.cardBorder)
                .attr('stroke-width', d => d.id === this.currentFuncId ? 2 : 1);

            // 标题区域
            this.nodeElements.append('text')
                .attr('class', 'card-title')
                .attr('x', CARD.padding)
                .attr('y', CARD.headerHeight)
                .attr('fill', THEME.titleColor)
                .attr('font-size', '16px')
                .attr('font-weight', '800')
                .attr('font-family', 'Roboto, sans-serif')
                .text(d => this._truncate(d.name, 24));

            // 分隔线1（标题下方）
            this.nodeElements.append('line')
                .attr('x1', CARD.padding - 2)
                .attr('y1', CARD.headerHeight + 6)
                .attr('x2', d => d.cardWidth - CARD.padding + 2)
                .attr('y2', CARD.headerHeight + 6)
                .attr('stroke', THEME.separatorColor)
                .attr('stroke-width', 1);

            if (isOverview) {
                // 概览层：描述 + 分隔线2 + 实体列表 + 标签
                this.nodeElements.append('text')
                    .attr('class', 'card-desc')
                    .attr('x', CARD.padding)
                    .attr('y', CARD.headerHeight + CARD.descLineHeight + 8)
                    .attr('fill', THEME.descColor)
                    .attr('font-size', '11px')
                    .attr('font-family', 'Roboto, sans-serif')
                    .text(d => d.funcCount + ' functions');

                // 分隔线2（描述下方）
                this.nodeElements.append('line')
                    .attr('x1', CARD.padding - 2)
                    .attr('y1', CARD.headerHeight + CARD.descLineHeight + 16)
                    .attr('x2', d => d.cardWidth - CARD.padding + 2)
                    .attr('y2', CARD.headerHeight + CARD.descLineHeight + 16)
                    .attr('stroke', THEME.separatorColor2)
                    .attr('stroke-width', 1);

                // 实体列表（前3个函数名）
                this.nodeElements.each((d, i, els) => {
                    const g = d3.select(els[i]);
                    d.topFuncs.forEach((fn, fi) => {
                        g.append('text')
                            .attr('x', CARD.padding + 8)
                            .attr('y', CARD.headerHeight + CARD.descLineHeight + 10 + (fi + 1) * CARD.entityLineHeight + 14)
                            .attr('fill', THEME.lightTextColor)
                            .attr('font-size', '10px')
                            .attr('font-family', 'monospace')
                            .attr('opacity', 0.8)
                            .text('• ' + this._truncate(fn, 22));
                    });
                });

                // "N calls" 标签
                this.nodeElements.append('rect')
                    .attr('x', CARD.padding - 2)
                    .attr('y', d => d.cardHeight - CARD.tagHeight - CARD.padding + 2)
                    .attr('width', d => {
                        const t = d.totalDeg + ' call' + (d.totalDeg !== 1 ? 's' : '');
                        return t.length * 7 + 12;
                    })
                    .attr('height', 18)
                    .attr('rx', 9)
                    .attr('fill', THEME.tagBg);

                this.nodeElements.append('text')
                    .attr('x', CARD.padding + 4)
                    .attr('y', d => d.cardHeight - CARD.tagHeight - CARD.padding + 14)
                    .attr('fill', THEME.tagColor)
                    .attr('font-size', '10px')
                    .attr('font-weight', '600')
                    .attr('font-family', 'Roboto, sans-serif')
                    .text(d => d.totalDeg + ' call' + (d.totalDeg !== 1 ? 's' : ''));

            } else {
                // 详情层：文件路径 + 分隔线2 + 标签
                this.nodeElements.append('text')
                    .attr('class', 'card-desc')
                    .attr('x', CARD.padding)
                    .attr('y', CARD.headerHeight + CARD.descLineHeight + 8)
                    .attr('fill', THEME.descColor)
                    .attr('font-size', '10px')
                    .attr('font-family', 'monospace')
                    .attr('opacity', 0.8)
                    .text(d => this._truncate(d.fileShort || d.file || '', 28));

                // 分隔线2（描述下方）
                this.nodeElements.append('line')
                    .attr('x1', CARD.padding - 2)
                    .attr('y1', CARD.headerHeight + CARD.descLineHeight + 16)
                    .attr('x2', d => d.cardWidth - CARD.padding + 2)
                    .attr('y2', CARD.headerHeight + CARD.descLineHeight + 16)
                    .attr('stroke', THEME.separatorColor2)
                    .attr('stroke-width', 1);

                // "N calls" 标签
                this.nodeElements.append('rect')
                    .attr('x', CARD.padding - 2)
                    .attr('y', d => d.cardHeight - CARD.tagHeight - CARD.padding + 2)
                    .attr('width', d => {
                        const t = d.weight + ' call' + (d.weight !== 1 ? 's' : '');
                        return t.length * 7 + 12;
                    })
                    .attr('height', 18)
                    .attr('rx', 9)
                    .attr('fill', THEME.tagBg);

                this.nodeElements.append('text')
                    .attr('x', CARD.padding + 4)
                    .attr('y', d => d.cardHeight - CARD.tagHeight - CARD.padding + 14)
                    .attr('fill', THEME.tagColor)
                    .attr('font-size', '10px')
                    .attr('font-weight', '600')
                    .attr('font-family', 'Roboto, sans-serif')
                    .text(d => d.weight + ' call' + (d.weight !== 1 ? 's' : ''));
            }

            // ── 交互 ──
            this.nodeElements
                .on('mouseenter', (event, d) => {
                    this.linkElements.select('line')
                        .attr('stroke', l => {
                            const sid = (typeof l.source === 'object') ? l.source.id : l.source;
                            const tid = (typeof l.target === 'object') ? l.target.id : l.target;
                            return (sid === d.id || tid === d.id) ? THEME.linkHighlight : THEME.linkColor;
                        })
                        .attr('stroke-width', l => {
                            const sid = (typeof l.source === 'object') ? l.source.id : l.source;
                            const tid = (typeof l.target === 'object') ? l.target.id : l.target;
                            return (sid === d.id || tid === d.id) ? 2.5 : 0.5;
                        })
                        .style('opacity', l => {
                            const sid = (typeof l.source === 'object') ? l.source.id : l.source;
                            const tid = (typeof l.target === 'object') ? l.target.id : l.target;
                            return (sid === d.id || tid === d.id) ? 1 : 0.1;
                        })
                        .attr('marker-end', l => {
                            const sid = (typeof l.source === 'object') ? l.source.id : l.source;
                            const tid = (typeof l.target === 'object') ? l.target.id : l.target;
                            return (sid === d.id || tid === d.id) ? 'url(#circle-end-hl)' : 'url(#circle-end)';
                        });

                    this.labelElements.style('opacity', l => {
                        const sid = (typeof l.source === 'object') ? l.source.id : l.source;
                        const tid = (typeof l.target === 'object') ? l.target.id : l.target;
                        return (sid === d.id || tid === d.id) ? 1 : 0.05;
                    });

                    d3.select(event.currentTarget).select('.card-bg')
                        .attr('stroke', THEME.cardBorderHover)
                        .style('filter', 'url(#card-glow)');
                })
                .on('mouseleave', (event, d) => {
                    this.linkElements.select('line')
                        .attr('stroke', THEME.linkColor)
                        .attr('stroke-width', l => Math.max(1.5, Math.min(3, Math.sqrt(l.count || 1) * 1.5)))
                        .style('opacity', 0.7)
                        .attr('marker-end', 'url(#circle-end)');

                    this.labelElements.style('opacity', 0.8);

                    d3.select(event.currentTarget).select('.card-bg')
                        .attr('stroke', THEME.cardBorder)
                        .style('filter', 'none');
                })
                .on('click', (event, d) => {
                    event.stopPropagation();
                    if (this.currentLevel === 'top') {
                        this._drawModule(d.dirPath);
                        if (window.G_CURRENT_VIEW === 'nebula' && window.focusModule) window.focusModule(d.dirPath);
                    } else if (this.currentLevel === 'module') {
                        this._drawFile(d.filePath);
                        if (window.G_CURRENT_VIEW === 'nebula' && window.focusFile) window.focusFile(d.filePath);
                    }
                })
                .on('contextmenu', (event, d) => {
                    event.preventDefault();
                    event.stopPropagation();
                    if (window.SourceAstra && window.SourceAstra.showContextMenu) {
                        const key = d.filePath || d.dirPath || d.id;
                        const docCtx = this._docContextForNode(d);
                        window.SourceAstra.showContextMenu(event.clientX, event.clientY, key, d.name, docCtx);
                    }
                });

            // ── Tick ──
            let fitted = false;
            this.simulation.on('tick', () => {
                this.linkElements.select('line')
                    .attr('x1', d => d.source.x + d.source.cardWidth / 2)
                    .attr('y1', d => d.source.y + d.source.cardHeight / 2)
                    .attr('x2', d => d.target.x + d.target.cardWidth / 2)
                    .attr('y2', d => d.target.y + d.target.cardHeight / 2);

                this.labelElements
                    .attr('x', d => (d.source.x + d.target.x) / 2 + d.source.cardWidth / 2)
                    .attr('y', d => (d.source.y + d.target.y) / 2 + d.source.cardHeight / 2);

                this.nodeElements.attr('transform', d => `translate(${d.x},${d.y})`);

                if (!fitted && this.simulation.alpha() < 0.05) {
                    fitted = true;
                    this._fitToNodes(nodes);
                }
            });
        }

        // ── 面包屑 ──
        _renderBreadcrumb() {
            if (window.G_CURRENT_VIEW !== 'explore' && window.G_CURRENT_VIEW !== 'trace') return;
            if (!this.dirMap) return;
            if (!this.breadcrumbEl) return;
            this.breadcrumbEl.innerHTML = '';

            const isValidLevel = this.currentLevel === 'file' || this.currentLevel === 'module';
            if (!isValidLevel && this.docDrawer) {
                this.docDrawer.classList.remove('active');
            }
            
            if (!this._bcMenuClickBound) {
                document.addEventListener('click', () => {
                    document.querySelectorAll('.explore-bc-menu').forEach(m => m.style.display = 'none');
                });
                this._bcMenuClickBound = true;
            }

            const renderCustomDropdown = (options, currentValue, onChange, placeholder = 'Select...') => {
                const wrap = document.createElement('div');
                wrap.style.cssText = 'position:relative; display:inline-block; margin-left:4px;';
                
                const btn = document.createElement('div');
                btn.style.cssText = `
                    background:${THEME.breadcrumbBg};
                    backdrop-filter:blur(4px);
                    border:1px solid ${THEME.breadcrumbActive};
                    padding:3px 10px;
                    border-radius:6px;
                    color:${THEME.breadcrumbActive};
                    font-size:11px;
                    cursor:pointer;
                    display:flex;
                    align-items:center;
                    gap:6px;
                    transition:all 0.2s;
                `;
                btn.onmouseenter = () => btn.style.borderColor = THEME.titleColor;
                btn.onmouseleave = () => btn.style.borderColor = THEME.breadcrumbActive;

                const currentOpt = options.find(o => o.value === currentValue);
                btn.innerHTML = `<span>${currentOpt ? currentOpt.label : placeholder}</span> <span style="font-size:8px;opacity:0.8">▼</span>`;
                
                const menu = document.createElement('div');
                menu.className = 'explore-bc-menu';
                menu.style.cssText = `
                    position:absolute;
                    top:100%;
                    left:0;
                    margin-top:6px;
                    background:var(--panel-bg);
                    backdrop-filter:blur(10px);
                    border:1px solid var(--panel-border);
                    border-radius:8px;
                    padding:6px;
                    display:none;
                    flex-direction:column;
                    max-height:300px;
                    overflow-y:auto;
                    z-index:100;
                    min-width:180px;
                    box-shadow:var(--shadow-lg);
                `;

                const searchInput = document.createElement('input');
                searchInput.type = 'text';
                searchInput.placeholder = 'Search...';
                searchInput.style.cssText = `
                    width:100%;
                    padding:6px;
                    margin-bottom:6px;
                    background:var(--bg-main, rgba(0,0,0,0.3));
                    border:1px solid var(--panel-border, rgba(255,255,255,0.1));
                    border-radius:4px;
                    color:var(--text-main, #fff);
                    font-size:11px;
                    outline:none;
                    box-sizing:border-box;
                `;
                searchInput.onclick = e => e.stopPropagation();
                menu.appendChild(searchInput);

                const listWrap = document.createElement('div');
                listWrap.style.cssText = 'display:flex;flex-direction:column;gap:2px;';
                menu.appendChild(listWrap);

                const renderList = (filterStr = '') => {
                    listWrap.innerHTML = '';
                    options.filter(o => o.label.toLowerCase().includes(filterStr.toLowerCase())).forEach(opt => {
                        const item = document.createElement('div');
                        item.style.cssText = `
                            padding:6px 8px;
                            border-radius:4px;
                            color:var(--text-main);
                            font-size:11px;
                            cursor:pointer;
                            transition:background 0.2s;
                        `;
                        item.textContent = opt.label;
                        if (opt.value === currentValue) item.style.color = THEME.breadcrumbActive;
                        item.onmouseenter = () => item.style.background = 'var(--hover-bg, rgba(255,255,255,0.1))';
                        item.onmouseleave = () => item.style.background = 'transparent';
                        item.onclick = (e) => {
                            e.stopPropagation();
                            menu.style.display = 'none';
                            onChange(opt.value);
                        };
                        listWrap.appendChild(item);
                    });
                };
                renderList();

                let _searchTimer = 0;
                searchInput.oninput = e => {
                    clearTimeout(_searchTimer);
                    _searchTimer = setTimeout(() => renderList(e.target.value), 150);
                };

                btn.onclick = (e) => {
                    e.stopPropagation();
                    const isVisible = menu.style.display === 'flex';
                    document.querySelectorAll('.explore-bc-menu').forEach(m => m.style.display = 'none');
                    if (!isVisible) {
                        menu.style.display = 'flex';
                        searchInput.value = '';
                        renderList();
                        searchInput.focus();
                    }
                };
                
                wrap.appendChild(btn);
                wrap.appendChild(menu);
                return wrap;
            };

            this.breadcrumb.forEach((item, idx) => {
                if (idx > 0) {
                    const sep = document.createElement('span');
                    sep.textContent = '›';
                    sep.style.cssText = `color:${THEME.textDim};font-size:12px;margin:0 4px;`;
                    this.breadcrumbEl.appendChild(sep);
                }

                // Level 2: Module Dropdown
                if (idx === 1 && ['dir', 'file', 'trace'].includes(this.currentLevel)) {
                    const options = Object.values(this.dirMap)
                        .sort((a, b) => a.name.localeCompare(b.name))
                        .map(d => ({ value: d.dirPath, label: d.name }));
                    const select = renderCustomDropdown(options, this.currentDir, (val) => {
                        this._drawModule(val);
                        if (window.G_CURRENT_VIEW === 'nebula' && window.focusModule) window.focusModule(val);
                        if (window.G_CURRENT_VIEW === 'trace') {
                            const e = document.getElementById('und-empty'); if(e) e.style.display = 'flex';
                            const l = document.querySelector('.und-layout'); if(l) l.style.display = 'none';
                        }
                    });
                    this.breadcrumbEl.appendChild(select);
                    return;
                }
                
                // Level 3: File Dropdown
                if (idx === 2 && ['dir', 'file', 'trace'].includes(this.currentLevel)) {
                    const currentDirData = this.dirMap[this.currentDir];
                    const files = currentDirData ? Object.values(currentDirData.files) : [];
                    const options = files
                        .sort((a, b) => a.name.localeCompare(b.name))
                        .map(f => ({ value: f.filePath, label: f.name }));
                    const select = renderCustomDropdown(options, this.currentFile, (val) => {
                        this._drawFile(val);
                        if (window.G_CURRENT_VIEW === 'nebula' && window.focusFile) window.focusFile(val);
                        if (window.G_CURRENT_VIEW === 'trace') {
                            const e = document.getElementById('und-empty'); if(e) e.style.display = 'flex';
                            const l = document.querySelector('.und-layout'); if(l) l.style.display = 'none';
                        }
                    });
                    this.breadcrumbEl.appendChild(select);
                    return;
                }
                
                // Level 4: Trace (Function) Dropdown
                if (idx === 3 && ['file', 'trace'].includes(this.currentLevel)) {
                    // Find all functions in current file
                    const currentDirData = this.dirMap[this.currentDir];
                    const fileData = currentDirData && currentDirData.files[this.currentFile];
                    let options = [];
                    if (fileData && fileData.functions) {
                        options = fileData.functions
                            .filter(n => n.type === 'function')
                            .sort((a, b) => a.name.localeCompare(b.name))
                            .map(n => ({ value: n.id, label: n.name }));
                    }
                    const select = renderCustomDropdown(options, this.currentFuncId, (val) => {
                        this.currentFuncId = val;
                        // 触发全局响应
                        const view = window.G_CURRENT_VIEW;
                        if (view === 'nebula' && window.focusNode) {
                            window.focusNode(val);
                            this._renderBreadcrumb();
                        } else if (view === 'trace' && window.SourceAstra && window.SourceAstra.initUnderstand) {
                            window.SourceAstra.initUnderstand(val);
                            this._renderBreadcrumb();
                        } else {
                            this._drawTrace(val);
                        }
                    });
                    this.breadcrumbEl.appendChild(select);
                    return;
                }

                const btn = document.createElement('span');
                btn.textContent = item.label;
                const isLast = idx === this.breadcrumb.length - 1;
                btn.style.cssText = `
                    background:${THEME.breadcrumbBg};
                    backdrop-filter:blur(4px);
                    border:1px solid ${isLast ? THEME.breadcrumbActive : THEME.separatorColor};
                    padding:3px 10px;
                    border-radius:6px;
                    color:${isLast ? THEME.breadcrumbActive : THEME.textDim};
                    font-size:11px;
                    cursor:pointer;
                    transition:all 0.2s;
                    font-family:Roboto,sans-serif;
                `;
                btn.onmouseenter = () => { btn.style.color = THEME.descColor; btn.style.borderColor = THEME.titleColor; };
                btn.onmouseleave = () => {
                    btn.style.color = isLast ? THEME.breadcrumbActive : THEME.textDim;
                    btn.style.borderColor = isLast ? THEME.breadcrumbActive : 'rgba(255,255,255,0.1)';
                };
                btn.onclick = () => {
                    item.action();
                    if (window.G_CURRENT_VIEW === 'nebula') {
                        if (idx === 0 && window.initGraph) window.initGraph(); // top
                        else if (idx === 1 && window.focusModule) window.focusModule(this.currentDir);
                        else if (idx === 2 && window.focusFile) window.focusFile(this.currentFile);
                        else if (idx === 3 && window.focusNode) window.focusNode(this.currentFuncId);
                    } else if (window.G_CURRENT_VIEW === 'trace') {
                        if (idx === 3 && window.SourceAstra && window.SourceAstra.initUnderstand) {
                            window.SourceAstra.initUnderstand(this.currentFuncId);
                        }
                    }
                };
                this.breadcrumbEl.appendChild(btn);
            });

            // 追加下一级的占位下拉框（展开小箭头）
            if (this.currentLevel === 'top' && this.dirMap) {
                const sep = document.createElement('span');
                sep.textContent = '›';
                sep.style.cssText = `color:${THEME.textDim};font-size:12px;margin:0 4px;`;
                this.breadcrumbEl.appendChild(sep);

                const options = Object.values(this.dirMap)
                    .sort((a, b) => a.name.localeCompare(b.name))
                    .map(d => ({ value: d.dirPath, label: d.name }));
                const select = renderCustomDropdown(options, null, (val) => {
                    this._drawModule(val);
                    if (window.G_CURRENT_VIEW === 'nebula' && window.focusModule) window.focusModule(val);
                }, '展开目录');
                this.breadcrumbEl.appendChild(select);
            } else if (this.currentLevel === 'module') {
                const sep = document.createElement('span');
                sep.textContent = '›';
                sep.style.cssText = `color:${THEME.textDim};font-size:12px;margin:0 4px;`;
                this.breadcrumbEl.appendChild(sep);

                const currentDirData = this.dirMap[this.currentDir];
                const files = currentDirData ? Object.values(currentDirData.files) : [];
                const options = files
                    .sort((a, b) => a.name.localeCompare(b.name))
                    .map(f => ({ value: f.filePath, label: f.name }));
                const select = renderCustomDropdown(options, null, (val) => {
                    this._drawFile(val);
                    if (window.G_CURRENT_VIEW === 'nebula' && window.focusFile) window.focusFile(val);
                }, '展开文件');
                this.breadcrumbEl.appendChild(select);
            } else if (this.currentLevel === 'file') {
                const sep = document.createElement('span');
                sep.textContent = '›';
                sep.style.cssText = `color:${THEME.textDim};font-size:12px;margin:0 4px;`;
                this.breadcrumbEl.appendChild(sep);

                const currentDirData = this.dirMap[this.currentDir];
                const fileData = currentDirData && currentDirData.files[this.currentFile];
                let options = [];
                if (fileData && fileData.functions) {
                    options = fileData.functions
                        .filter(n => n.type === 'function')
                        .sort((a, b) => a.name.localeCompare(b.name))
                        .map(n => ({ value: n.id, label: n.name }));
                }
                const select = renderCustomDropdown(options, null, (val) => {
                    this.currentFuncId = val;
                    const view = window.G_CURRENT_VIEW;
                    if (view === 'nebula' && window.focusNode) {
                        window.focusNode(val);
                        this._renderBreadcrumb();
                    } else if (view === 'trace' && window.SourceAstra && window.SourceAstra.initUnderstand) {
                        window.SourceAstra.initUnderstand(val);
                        this._renderBreadcrumb();
                    } else {
                        this._drawTrace(val);
                    }
                }, '选择函数');
                this.breadcrumbEl.appendChild(select);
            }

            if (this.currentLevel === 'file' || this.currentLevel === 'module') {
                const sep = document.createElement('span');
                sep.textContent = '›';
                sep.style.cssText = `color:${THEME.textDim};font-size:12px;margin:0 4px;`;
                this.breadcrumbEl.appendChild(sep);

                const docBtn = document.createElement('span');
                docBtn.id = 'explore-doc-btn-toggle-inline';
                docBtn.style.cssText = `
                    background: ${THEME.breadcrumbBg};
                    backdrop-filter: blur(4px);
                    border: 1px dashed ${THEME.textDim}80;
                    padding: 3px 10px;
                    border-radius: 6px;
                    color: ${THEME.textDim};
                    font-size: 11px;
                    cursor: pointer;
                    display: inline-flex;
                    align-items: center;
                    gap: 4px;
                    transition: all 0.2s;
                    font-family: Roboto, sans-serif;
                `;
                docBtn.innerHTML = `<span>📖</span> <span id="explore-doc-btn-text">读取中...</span>`;
                
                docBtn.onclick = () => {
                    const isActive = this.docDrawer.classList.contains('active');
                    if (isActive) {
                        this.docDrawer.classList.remove('active');
                    } else {
                        this.docDrawer.classList.add('active');
                        this.loadDocHistoryAndContent();
                    }
                };
                
                this.breadcrumbEl.appendChild(docBtn);
                this.updateInlineDocButtonState(docBtn);
            }

            // ===== 渲染左侧导航侧边栏 =====
            if (window.G_CURRENT_VIEW !== 'explore' && window.G_CURRENT_VIEW !== 'nebula' && window.G_CURRENT_VIEW !== 'trace') {
                return;
            }
            const sidebarTitle = document.querySelector('#th h2');
            const sidebarListContainer = document.getElementById('tc');
            if (sidebarListContainer) {
                sidebarListContainer.innerHTML = '';
                sidebarListContainer.className = 'sidebar-nav-list';
                
                if (this.currentLevel === 'top') {
                    if (sidebarTitle) sidebarTitle.textContent = (window.i18n.locale === 'zh' ? '目录' : 'Directories') + ` (${Object.keys(this.dirMap).length})`;
                    
                    Object.values(this.dirMap).sort((a, b) => a.name.localeCompare(b.name)).forEach(d => {
                        const m = d.dirPath;
                        const name = d.name;
                        const fileCount = Object.keys(d.files).length;

                        const item = document.createElement('div');
                        item.className = 'sidebar-nav-item';
                        if (this.currentDir === m) item.classList.add('active');
                        
                        item.innerHTML = `<span>📦 ${name}</span><span class="badge">${fileCount} ${window.i18n.locale === 'zh' ? '文件' : 'Files'}</span>`;
                        item.onclick = () => {
                            this._drawModule(m);
                            if (window.G_CURRENT_VIEW === 'nebula' && window.focusModule) window.focusModule(m);
                        };
                        sidebarListContainer.appendChild(item);
                    });
                } else if (this.currentLevel === 'module') {
                    const activeModName = this.dirMap[this.currentDir]?.name || this.currentDir;
                    const files = this.dirMap[this.currentDir] ? Object.values(this.dirMap[this.currentDir].files) : [];
                    if (sidebarTitle) sidebarTitle.textContent = `${activeModName} (${files.length})`;
                    
                    files.sort((a, b) => a.name.localeCompare(b.name)).forEach(f => {
                        const name = f.name;
                        const filePath = f.filePath;
                        const item = document.createElement('div');
                        item.className = 'sidebar-nav-item';
                        if (this.currentFile === filePath) item.classList.add('active');
                        
                        item.innerHTML = `<span>📄 ${name}</span>`;
                        item.onclick = () => {
                            this._drawFile(filePath);
                            if (window.G_CURRENT_VIEW === 'nebula' && window.focusFile) window.focusFile(filePath);
                        };
                        sidebarListContainer.appendChild(item);
                    });
                } else if (this.currentLevel === 'file' || this.currentLevel === 'trace') {
                    const currentDirData = this.dirMap[this.currentDir];
                    const fileData = currentDirData && currentDirData.files[this.currentFile];
                    const activeFileName = fileData?.name || this.currentFile;
                    let functions = [];
                    if (fileData && fileData.functions) {
                        functions = fileData.functions
                            .filter(n => n.type === 'function')
                            .sort((a, b) => a.name.localeCompare(b.name));
                    }

                    if (sidebarTitle) sidebarTitle.textContent = `${activeFileName} (${functions.length})`;

                    functions.forEach(f => {
                        const name = f.name;
                        const id = f.id;
                        const item = document.createElement('div');
                        item.className = 'sidebar-nav-item';
                        if (this.currentFuncId === id) item.classList.add('active');

                        item.innerHTML = `<span class="sa-func-icon">fn</span><span>${name}</span>`;
                        item.onclick = () => {
                            this.currentFuncId = id;
                            const view = window.G_CURRENT_VIEW;
                            if (view === 'nebula' && window.focusNode) {
                                window.focusNode(id);
                                this._renderBreadcrumb();
                            } else if (view === 'trace' && window.SourceAstra && window.SourceAstra.initUnderstand) {
                                window.SourceAstra.initUnderstand(id);
                                this._renderBreadcrumb();
                            } else {
                                this._drawTrace(id);
                            }
                            // 刷新侧边栏高亮态
                            const activeItems = sidebarListContainer.querySelectorAll('.sidebar-nav-item');
                            activeItems.forEach(ai => ai.classList.remove('active'));
                            item.classList.add('active');
                        };
                        sidebarListContainer.appendChild(item);
                    });
                }
            }
        }

        _fitToNodes(nodes) {
            if (!nodes || nodes.length === 0) return;
            let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
            nodes.forEach(n => {
                if (n.x != null) {
                    minX = Math.min(minX, n.x);
                    maxX = Math.max(maxX, n.x + (n.cardWidth || 0));
                }
                if (n.y != null) {
                    minY = Math.min(minY, n.y);
                    maxY = Math.max(maxY, n.y + (n.cardHeight || 0));
                }
            });
            if (minX === Infinity) return;

            const w = maxX - minX || 1;
            const h = maxY - minY || 1;
            let scale = Math.min(this.width / w, this.height / h) * 0.85;
            // 限制最大缩放比例为 1.0，防止单个或少数节点时被过度放大，导致显示失真
            if (scale > 1.0) scale = 1.0;
            const tx = this.width / 2 - (minX + maxX) / 2 * scale;
            const ty = this.height / 2 - (minY + maxY) / 2 * scale;

            const newTransform = d3.zoomIdentity.translate(tx, ty).scale(scale);
            if (this.svg && this.zoom) {
                this.svg.transition().duration(500).call(this.zoom.transform, newTransform);
            }
        }

        async waitForRawData() {
            if (this.rawData) return;
            if (!this.loading) {
                await this.load();
            }
            return new Promise((resolve) => {
                const check = () => {
                    if (this.rawData) {
                        resolve();
                    } else {
                        setTimeout(check, 100);
                    }
                };
                check();
            });
        }

        _docContextForNode(node) {
            if (!node) return null;
            const targets = [];
            const addTarget = (docType, docKey) => {
                if (!docKey) return;
                if (targets.some(t => t.docType === docType && t.docKey === docKey)) return;
                targets.push({ docType, docKey, autoGenerate: false });
            };

            if (this.currentLevel === 'top') {
                const key = node.dirPath || node.id;
                addTarget('module', key);
            } else if (this.currentLevel === 'module') {
                const fileKey = node.filePath || node.id;
                const moduleKey = node.dirPath || this.currentDir || ((this.fileMap && this.fileMap[fileKey]) ? this.fileMap[fileKey].dirPath : '');
                addTarget('file', fileKey);
                addTarget('module', moduleKey);
            } else {
                const fileKey = node.filePath || this.currentFile;
                const moduleKey = node.dirPath || this.currentDir || ((this.fileMap && this.fileMap[fileKey]) ? this.fileMap[fileKey].dirPath : '');
                addTarget('file', fileKey);
                addTarget('module', moduleKey);
            }

            if (targets.length === 0) return null;
            return {
                docTargets: targets,
                docType: targets[0].docType,
                docKey: targets[0].docKey,
                autoGenerate: false
            };
        }

        async openDocDrawer(type, key, options = {}) {
            if (!type || !key) return;
            if (window.G_CURRENT_VIEW !== 'explore') {
                switchView('explore');
            }
            await this.waitForRawData();
            
            if (type === 'file') {
                this.currentLevel = 'file';
                this.currentFile = key;
                this.currentDir = (this.fileMap && this.fileMap[key]) ? this.fileMap[key].dirPath : null;
                this.currentFuncId = null;
            } else if (type === 'module') {
                this.currentLevel = 'module';
                this.currentDir = key;
                this.currentFile = null;
                this.currentFuncId = null;
            } else if (type === 'project') {
                this.currentLevel = 'top';
                this.currentDir = '__project__';
                this.currentFile = null;
                this.currentFuncId = null;
            }
            
            if (this.docDrawer) {
                this.docDrawer.classList.add('active');
                this.renderDocActionPanel(type, key, options.autoGenerate ? '正在准备生成...' : '');
                if (options.autoGenerate) {
                    this.docTitle.textContent = type === 'file'
                        ? `文件: ${String(key).split('/').pop()}`
                        : type === 'project' ? '项目理解文档' : `目录: ${this._dirDisplayName(key)}`;
                    await Promise.resolve();
                    this.renderDocGeneratingState('正在准备代码上下文...');
                    if (type === 'file') {
                        await Promise.resolve(this._drawFile(key));
                    } else if (type === 'module') {
                        await Promise.resolve(this._drawModule(key));
                    } else if (type === 'project') {
                        await Promise.resolve(this._drawOverview());
                    }
                    await this.generateDoc();
                } else {
                    if (type === 'file') {
                        await Promise.resolve(this._drawFile(key));
                    } else if (type === 'module') {
                        await Promise.resolve(this._drawModule(key));
                    } else if (type === 'project') {
                        await Promise.resolve(this._drawOverview());
                    }
                    this.loadDocHistoryAndContent();
                }
            }
        }

        _getEmptyDocHtml(type, hint) {
            return `
                <div class="explore-doc-empty-container">
                    <div class="explore-doc-empty-icon-wrapper">
                        <div class="explore-doc-empty-svg-glow"></div>
                        <svg class="explore-doc-empty-svg" viewBox="0 0 100 100" fill="none" xmlns="http://www.w3.org/2000/svg">
                            <circle cx="50" cy="50" r="40" stroke="url(#empty-svg-grad)" stroke-width="2" stroke-dasharray="10 15" stroke-linecap="round">
                                <animateTransform attributeName="transform" type="rotate" from="0 50 50" to="360 50 50" dur="8s" repeatCount="indefinite"/>
                            </circle>
                            <circle cx="50" cy="50" r="30" stroke="url(#empty-svg-grad2)" stroke-width="1.5" stroke-dasharray="25 8" stroke-linecap="round" opacity="0.6">
                                <animateTransform attributeName="transform" type="rotate" from="360 50 50" to="0 50 50" dur="6s" repeatCount="indefinite"/>
                            </circle>
                            <circle cx="50" cy="50" r="9" fill="url(#empty-svg-grad)" opacity="0.8">
                                <animate attributeName="r" values="8;11;8" dur="3s" repeatCount="indefinite"/>
                            </circle>
                            <path d="M50 20 L50 12" stroke="#6366f1" stroke-width="2" stroke-linecap="round" opacity="0.7"/>
                            <path d="M50 80 L50 88" stroke="#ec4899" stroke-width="2" stroke-linecap="round" opacity="0.7"/>
                            <path d="M20 50 L12 50" stroke="#38bdf8" stroke-width="2" stroke-linecap="round" opacity="0.7"/>
                            <path d="M80 50 L88 50" stroke="#a855f7" stroke-width="2" stroke-linecap="round" opacity="0.7"/>
                            <defs>
                                <linearGradient id="empty-svg-grad" x1="0" y1="0" x2="100" y2="100" gradientUnits="userSpaceOnUse">
                                    <stop offset="0%" stop-color="#38bdf8" />
                                    <stop offset="50%" stop-color="#6366f1" />
                                    <stop offset="100%" stop-color="#ec4899" />
                                </linearGradient>
                                <linearGradient id="empty-svg-grad2" x1="100" y1="0" x2="0" y2="100" gradientUnits="userSpaceOnUse">
                                    <stop offset="0%" stop-color="#a855f7" />
                                    <stop offset="100%" stop-color="#6366f1" />
                                </linearGradient>
                            </defs>
                        </svg>
                    </div>
                    <div class="explore-doc-empty-title">暂无此${type === 'file' ? '文件' : '模块'}的理解文档</div>
                    <div class="explore-doc-empty-desc">
                        ${hint || '通过大语言模型直接阅读与分析该代码或结构，提炼其核心职责、架构设计分工与关键协作流。'}
                    </div>
                    <button type="button" class="explore-doc-btn" id="explore-doc-generate-btn" style="width:100%;">
                        <span>⚡</span> 一键生成理解文档
                    </button>
                </div>
            `;
        }

        renderDocActionPanel(type, key, hint = '') {
            if (!this.docContent) return;
            this.docTitle.textContent = type === 'file'
                ? `文件: ${String(key).split('/').pop()}`
                : type === 'project' ? '项目理解文档' : `目录: ${this._dirDisplayName(key)}`;
            
            this.docContent.innerHTML = this._getEmptyDocHtml(type, hint);

        }

        renderDocGeneratingState(message = '正在提取代码上下文...') {
            if (!this.docContent) return;
            
            // 依据步骤文案判定当前执行进度
            let step1Class = 'active', step2Class = '', step3Class = '', step4Class = '';
            
            if (message.includes('呼叫') || message.includes('分析')) {
                step1Class = 'done';
                step2Class = 'active';
            } else if (message.includes('保存') || message.includes('持久化')) {
                step1Class = 'done';
                step2Class = 'done';
                step3Class = 'active';
            } else if (message.includes('排版') || message.includes('渲染')) {
                step1Class = 'done';
                step2Class = 'done';
                step3Class = 'done';
                step4Class = 'active';
            }

            const activeModel = window.getScopedLlmModelSlot
                ? window.getScopedLlmModelSlot('GLOBAL')
                : (localStorage.getItem('G_PRIMARY_MODEL') || localStorage.getItem('G_ACTIVE_MODEL') || 'deepseek-v3');
            const configs = window.getLlmConfigs ? window.getLlmConfigs() : {};
            const conf = configs[activeModel] || { name: 'DeepSeek-V3', model: 'deepseek-chat', url: '', key: '', temp: '30' };
            const timeoutSeconds = window.getLlmTimeoutSeconds ? window.getLlmTimeoutSeconds(conf) : 300;
            const retryCount = window.getLlmRetryCount ? window.getLlmRetryCount(conf) : 1;
            const globalPolicy = window.getLlmReasoningPolicy ? window.getLlmReasoningPolicy(conf) : { enabled: true, effort: 'medium', visibility: 'summary' };
            const override = (window.SA_REASONING_OVERRIDES && window.SA_REASONING_OVERRIDES.explore) || { enabled: null, effort: null, visibility: null };
            const activeEnabled = override.enabled !== null ? override.enabled : globalPolicy.enabled;
            const activeEffort = override.effort !== null ? override.effort : globalPolicy.effort;
            const isOverride = override.enabled !== null || override.effort !== null || override.visibility !== null;
            
            const effortMap = { low: '低', medium: '标准', high: '深度', custom: '自定义' };
            const activeEffortText = effortMap[activeEffort] || '标准';
            const reasoningModeText = activeEnabled ? `开启 (${activeEffortText})` : '未开启';
            
            const metaBorder = isOverride ? '1px solid var(--chat-success, #10b981)' : '1px solid rgba(255,255,255,0.05)';
            const metaBg = isOverride ? 'rgba(16, 185, 129, 0.08)' : 'rgba(255,255,255,0.02)';
            const metaColor = isOverride ? 'var(--chat-success, #10b981)' : THEME.textDim;

            this.docContent.innerHTML = `
                <div class="explore-doc-loading-wrapper">
                    <div class="explore-doc-pulse-ring">
                        <div class="pulse-circle"></div>
                    </div>
                    
                    <div style="text-align:center; margin-top:-8px;">
                        <div style="font-size:13.5px; font-weight:700; color:#38bdf8;" id="gen-step-msg">${this.escapeHtml(message)}</div>
                        <div style="font-size:11px; color:${THEME.textDim}; margin-top:4px;">这通常需要 15 ~ 30 秒，请稍候...</div>
                    </div>

                    <!-- 大模型与推理属性栏 -->
                    <div class="explore-doc-llm-meta" style="margin: 14px auto 16px; padding: 6px 10px; width: fit-content; background: ${metaBg}; border: ${metaBorder}; border-radius: 6px; display: flex; align-items: center; justify-content: center; gap: 8px; font-size: 11px; color: ${metaColor}; cursor: pointer; user-select: none;" title="点击动态调整单次任务推理策略">
                        <span>🤖 模型: <strong style="color: var(--chat-primary, #818cf8);">${conf.name || activeModel}</strong></span>
                        <span style="opacity: 0.3;">|</span>
                        <span>⚡ 推理: <strong style="color: ${activeEnabled ? 'var(--chat-success, #10b981)' : 'inherit'};">${reasoningModeText}${isOverride ? ' (覆写)' : ''}</strong></span>
                        <span style="opacity: 0.3;">|</span>
                        <span>⏱️ 超时: <strong>${timeoutSeconds}s</strong></span>
                        <span style="opacity: 0.3;">|</span>
                        <span>🔄 重试限制: <strong>${retryCount}次</strong></span>
                    </div>
                    
                    <div class="explore-doc-steps">
                        <div class="explore-doc-step-item ${step1Class}">
                            <div class="step-dot"></div>
                            <span>1. 提取源码结构与上下文准备</span>
                        </div>
                        <div class="explore-doc-step-item ${step2Class}">
                            <div class="step-dot"></div>
                            <span>2. 呼叫大语言模型智能推理</span>
                        </div>
                        <div class="explore-doc-step-item ${step3Class}">
                            <div class="step-dot"></div>
                            <span>3. 归纳架构设计职责与依赖链</span>
                        </div>
                        <div class="explore-doc-step-item ${step4Class}">
                            <div class="step-dot"></div>
                            <span>4. 本地持久化与Markdown排版</span>
                        </div>
                    </div>
                    
                    <div class="explore-doc-skeleton-container">
                        <div class="explore-doc-skeleton-bar" style="width: 85%;"></div>
                        <div class="explore-doc-skeleton-bar" style="width: 100%;"></div>
                        <div class="explore-doc-skeleton-bar" style="width: 70%;"></div>
                    </div>
                </div>
            `;
            this.ensureDocSpinStyle();
        }

        ensureDocSpinStyle() {
            if (document.getElementById('explore-spin-style')) return;
            const style = document.createElement('style');
            style.id = 'explore-spin-style';
            style.innerHTML = `@keyframes spin { to { transform: rotate(360deg); } }`;
            document.head.appendChild(style);
        }

        async fetchWithTimeout(url, options = {}, timeoutMs = 300000) {
            const controller = new AbortController();
            if (window.saRegisterActiveLlmRequest) window.saRegisterActiveLlmRequest(controller);
            const timer = setTimeout(() => controller.abort(), timeoutMs);
            try {
                return await fetch(url, { ...options, signal: controller.signal });
            } catch (err) {
                if (err && err.name === 'AbortError') {
                    throw new Error(`生成请求超时，已等待 ${Math.round(timeoutMs / 1000)} 秒。`);
                }
                throw err;
            } finally {
                clearTimeout(timer);
                if (window.saUnregisterActiveLlmRequest) window.saUnregisterActiveLlmRequest(controller);
            }
        }

        async fetchChatCompletionWithRetry(url, options, timeoutMs = 300000, maxRetries = 2) {
            let lastError = null;
            for (let attempt = 0; attempt <= maxRetries; attempt++) {
                if (attempt > 0) {
                    const stepMsg = this.container.querySelector('#gen-step-msg');
                    if (stepMsg) stepMsg.textContent = `模型服务暂时异常，正在重试第 ${attempt} 次...`;
                    await new Promise(resolve => setTimeout(resolve, attempt * 1200));
                }

                try {
                    const response = await this.fetchWithTimeout(url, options, timeoutMs);
                    const text = await response.text();
                    let body = null;
                    if (text) {
                        try {
                            body = JSON.parse(text);
                        } catch (_) {
                            body = { raw: text };
                        }
                    }
                    if (response.ok) {
                        return body;
                    }

                    const message = body && body.error && body.error.message
                        ? body.error.message
                        : (body && body.raw ? body.raw : text);
                    lastError = new Error(`LLM 响应错误 HTTP ${response.status}: ${message || response.statusText}`);
                    if (response.status < 500 || attempt === maxRetries) {
                        throw lastError;
                    }
                } catch (err) {
                    lastError = err;
                    const retriable = err && /超时|Failed to fetch|NetworkError|EOF|server_error|HTTP 5\d\d/i.test(err.message || String(err));
                    if (!retriable || attempt === maxRetries) {
                        throw err;
                    }
                }
            }
            throw lastError || new Error('模型服务请求失败');
        }

        ensureDocDrawerStyle() {
            if (document.getElementById('explore-doc-drawer-style')) return;
            const style = document.createElement('style');
            style.id = 'explore-doc-drawer-style';
            style.innerHTML = `
                /* 本身的大量样式已统一在 modules/explore.css 中实现 */
                .explore-doc-drawer {
                    z-index: 200 !important;
                }
            `;
            document.head.appendChild(style);
        }

        initDocDrawer() {
            if (!this.docDrawer || !this.docCloseBtn) return;
            this.docCloseBtn.onclick = () => {
                this.docDrawer.classList.remove('active');
            };

            if (this.docContent) {
                this.docContent.addEventListener('click', (e) => {
                    const btn = e.target.closest('#explore-doc-generate-btn, #explore-doc-regenerate-btn, #explore-doc-retry-btn');
                    if (btn && this.docContent.contains(btn)) {
                        e.preventDefault();
                        e.stopPropagation();
                        this.generateDoc();
                        return;
                    }

                    // 委托绑定推理策略覆写 Badge 点击
                    const badge = e.target.closest('#explore-reasoning-override-badge');
                    if (badge && this.docContent.contains(badge)) {
                        e.preventDefault();
                        e.stopPropagation();
                        if (window.showReasoningOverridePopover) {
                            window.showReasoningOverridePopover(e, 'explore');
                        }
                        return;
                    }

                    // 委托绑定生成时推理 Meta 栏点击
                    const meta = e.target.closest('.explore-doc-llm-meta');
                    if (meta && this.docContent.contains(meta)) {
                        e.preventDefault();
                        e.stopPropagation();
                        if (window.showReasoningOverridePopover) {
                            window.showReasoningOverridePopover(e, 'explore');
                        }
                        return;
                    }
                });
            }

            window.G_EXPLORE_REFRESH_META = () => {
                this.updateExploreReasoningBadge();
            };

            const resizer = this.container.querySelector('#explore-doc-resizer');
            if (resizer) {
                let startX = 0, startWidth = 0;
                const onMouseMove = (e) => {
                    const dx = e.clientX - startX;
                    const newW = Math.max(300, Math.min(window.innerWidth - 250, startWidth - dx));
                    this.docDrawer.style.width = newW + 'px';
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
                    startWidth = this.docDrawer.offsetWidth;
                    resizer.classList.add('dragging');
                    document.body.classList.add('resizing');
                    document.addEventListener('mousemove', onMouseMove);
                    document.addEventListener('mouseup', onMouseUp);
                });
            }
        }

        async updateDocButtonState() {
            const docBtn = document.getElementById('explore-doc-btn-toggle-inline');
            if (docBtn) {
                await this.updateInlineDocButtonState(docBtn);
            }
        }

        async updateInlineDocButtonState(docBtn) {
            if (!docBtn) return;
            const isValidLevel = this.currentLevel === 'file' || this.currentLevel === 'module';
            if (!isValidLevel) return;

            const type = this.currentLevel === 'file' ? 'file' : (this.currentDir === '__project__' ? 'project' : 'module');
            const key = this.currentLevel === 'file' ? this.currentFile : this.currentDir;
            const textEl = docBtn.querySelector('#explore-doc-btn-text');

            try {
                const res = await fetch(`/api/documents/list?type=${type}&key=${encodeURIComponent(key)}`, {
                    headers: window.saAuthHeaders ? window.saAuthHeaders() : {}
                });
                if (res.ok) {
                    const list = await res.json();
                    if (list && list.length > 0) {
                        textEl.textContent = `理解文档 [已生成]`;
                        docBtn.style.borderColor = THEME.breadcrumbActive;
                        docBtn.style.color = THEME.breadcrumbActive;
                        docBtn.style.borderStyle = 'solid';
                        
                        docBtn.onmouseenter = () => { 
                            docBtn.style.color = THEME.descColor; 
                            docBtn.style.borderColor = THEME.titleColor; 
                        };
                        docBtn.onmouseleave = () => { 
                            docBtn.style.color = THEME.breadcrumbActive; 
                            docBtn.style.borderColor = THEME.breadcrumbActive; 
                        };
                    } else {
                        textEl.textContent = '生成理解文档';
                        docBtn.style.borderColor = THEME.textDim + '80';
                        docBtn.style.color = THEME.textDim;
                        docBtn.style.borderStyle = 'dashed';
                        
                        docBtn.onmouseenter = () => { 
                            docBtn.style.color = THEME.breadcrumbActive; 
                            docBtn.style.borderColor = THEME.breadcrumbActive;
                            docBtn.style.borderStyle = 'solid';
                        };
                        docBtn.onmouseleave = () => { 
                            docBtn.style.color = THEME.textDim; 
                            docBtn.style.borderColor = THEME.textDim + '80';
                            docBtn.style.borderStyle = 'dashed';
                        };
                    }
                }
            } catch (e) {
                console.error('[Explore] Failed to get doc list:', e);
                textEl.textContent = '理解文档';
            }
        }

        async loadDocHistoryAndContent(targetTimestamp = '') {
            if (!this.docContent) return;
            const type = this.currentLevel === 'file' ? 'file' : (this.currentDir === '__project__' ? 'project' : 'module');
            const key = this.currentLevel === 'file' ? this.currentFile : this.currentDir;
            const title = this.currentLevel === 'file' ? `文件: ${key.split('/').pop()}` : (type === 'project' ? '项目理解文档' : `目录: ${this._dirDisplayName(key)}`);

            // 如果不是刚刚生成而是人为点击其他版本，则清空上次产生的推理摘要
            if (targetTimestamp !== '') {
                this.lastReasoningSummary = null;
            }

            this.docTitle.textContent = title;
            this.docContent.innerHTML = `<div style="text-align:center;padding:40px;color:${THEME.textDim};">加载中...</div>`;

            try {
                const listRes = await fetch(`/api/documents/list?type=${type}&key=${encodeURIComponent(key)}`, {
                    headers: window.saAuthHeaders ? window.saAuthHeaders() : {}
                });
                let historyList = [];
                if (listRes.ok) {
                    historyList = await listRes.json();
                }

                let url = `/api/documents/get?type=${type}&key=${encodeURIComponent(key)}`;
                if (targetTimestamp) {
                    url += `&timestamp=${encodeURIComponent(targetTimestamp)}`;
                }
                const contentRes = await fetch(url, {
                    headers: window.saAuthHeaders ? window.saAuthHeaders() : {}
                });
                let docData = { content: "", timestamp: "" };
                if (contentRes.ok) {
                    docData = await contentRes.json();
                }

                this.renderDocArea(docData, historyList, targetTimestamp || docData.timestamp);
            } catch (e) {
                this.docContent.innerHTML = `<div style="color:#ef4444;padding:20px;">加载出错: ${e.message}</div>`;
            }
        }

        renderDocArea(docData, historyList, activeTimestamp) {
            const hasDoc = docData && docData.content && docData.content.trim() !== "";
            
            let html = '';

            if (hasDoc) {
                let reasoningSummaryHtml = '';

                html += `
                    <div class="explore-doc-config explore-doc-sticky-bar">
                        <div style="display:flex; justify-content:space-between; align-items:center; gap:8px;">
                            <div style="display:flex; align-items:center; gap:4px;">
                                <span style="font-size:11px;color:${THEME.textDim};">版本:</span>
                                <select class="explore-doc-select" id="explore-doc-version-select" style="min-width:110px; height:28px; padding:2px 20px 2px 8px;">
                                    ${historyList.map(item => `
                                        <option value="${item.timestamp}" ${item.timestamp === activeTimestamp ? 'selected' : ''}>
                                            ${item.timestamp}
                                        </option>
                                    `).join('')}
                                </select>
                            </div>
                            <div id="explore-reasoning-override-badge" style="display:inline-flex; align-items:center; gap:4px; cursor:pointer; background:rgba(99,102,241,0.08); border:1px solid rgba(99,102,241,0.25); color:#818cf8; padding:3px 8px; border-radius:6px; font-size:10.5px; transition:all 0.2s; user-select:none;" title="点击动态调整单次任务推理策略">
                                <span>⚡ 推理: 开启 (默认)</span>
                            </div>
                        </div>
                        <div style="display:flex; gap:8px; margin-top:4px; width:100%;">
                            <button type="button" class="explore-doc-btn" id="explore-doc-regenerate-btn" style="flex:1; min-height:30px; padding:4px 12px;">
                                <span>⚡</span> 重新生成
                            </button>
                            <button type="button" class="explore-doc-btn" id="explore-doc-export-btn" style="flex:1; min-height:30px; padding:4px 12px; background:rgba(16,185,129,0.15); border-color:rgba(16,185,129,0.3); color:#10b981;">
                                <span>📤</span> 导出
                            </button>
                        </div>
                    </div>
                    ${reasoningSummaryHtml}
                    <div class="explore-doc-markdown" id="explore-doc-markdown-render">
                        <div style="text-align:center;padding:20px;color:${THEME.textDim};">排版渲染中...</div>
                    </div>
                    <div class="explore-doc-footer-panel">
                        <button class="explore-doc-footer-btn" id="explore-doc-copy-all-btn" style="width: 100%;">
                            <span>📋</span> 复制 Markdown 全文
                        </button>
                    </div>
                `;
            } else {
                html += this._getEmptyDocHtml(this.currentLevel === 'file' ? 'file' : 'module');
            }

            this.docContent.innerHTML = html;

            const versionSelect = this.docContent.querySelector('#explore-doc-version-select');
            if (versionSelect) {
                versionSelect.onchange = (e) => {
                    this.loadDocHistoryAndContent(e.target.value);
                };
            }

            // 导出文档绑定
            const exportBtn = this.docContent.querySelector('#explore-doc-export-btn');
            if (exportBtn && hasDoc) {
                exportBtn.onclick = () => {
                    const type = this.currentLevel === 'file' ? 'file' : (this.currentDir === '__project__' ? 'project' : 'module');
                    const key = this.currentLevel === 'file' ? this.currentFile : this.currentDir;
                    const displayName = type === 'file' ? key.split('/').pop() : (type === 'project' ? 'project' : this._dirDisplayName(key));
                    
                    const blob = new Blob([docData.content], { type: 'text/markdown;charset=utf-8' });
                    const a = document.createElement('a');
                    a.download = `${displayName}_architecture_doc_${activeTimestamp || Date.now()}.md`;
                    a.href = URL.createObjectURL(blob);
                    a.click();
                    URL.revokeObjectURL(a.href);
                };
            }

            // 全文复制绑定
            const copyAllBtn = this.docContent.querySelector('#explore-doc-copy-all-btn');
            if (copyAllBtn && hasDoc) {
                copyAllBtn.onclick = () => {
                    navigator.clipboard.writeText(docData.content).then(() => {
                        const originalText = copyAllBtn.innerHTML;
                        copyAllBtn.innerHTML = '<span>✅</span> 全文复制成功';
                        setTimeout(() => {
                            copyAllBtn.innerHTML = originalText;
                        }, 2000);
                    }).catch(err => {
                        console.error('Failed to copy text:', err);
                    });
                };
            }

            const mdRenderEl = this.docContent.querySelector('#explore-doc-markdown-render');
            if (mdRenderEl && hasDoc) {
                this.renderDocMarkdown(docData.content, mdRenderEl).catch((err) => {
                    console.error('[Explore] Markdown render failed:', err);
                    mdRenderEl.innerHTML = `
                        <div style="color:#ef4444;padding:16px;border:1px solid rgba(239,68,68,0.25);border-radius:8px;margin-bottom:12px;">
                            文档排版失败，已切换为原文显示: ${this.escapeHtml(err.message || String(err))}
                        </div>
                        <pre style="white-space:pre-wrap;word-break:break-word;color:${THEME.text};background:rgba(15,23,42,0.55);padding:16px;border-radius:8px;">${this.escapeHtml(docData.content)}</pre>
                    `;
                });
            }
            this.updateExploreReasoningBadge();
        }

        updateExploreReasoningBadge() {
            const badge = this.docContent ? this.docContent.querySelector('#explore-reasoning-override-badge') : null;
            if (!badge) return;
            const activeModel = window.getScopedLlmModelSlot
                ? window.getScopedLlmModelSlot('GLOBAL')
                : (localStorage.getItem('G_PRIMARY_MODEL') || localStorage.getItem('G_ACTIVE_MODEL') || 'deepseek-v3');
            const configs = window.getLlmConfigs ? window.getLlmConfigs() : {};
            const conf = configs[activeModel] || {};
            const globalPolicy = window.getLlmReasoningPolicy ? window.getLlmReasoningPolicy(conf) : { enabled: true, effort: 'medium', visibility: 'summary' };
            
            const override = (window.SA_REASONING_OVERRIDES && window.SA_REASONING_OVERRIDES.explore) || { enabled: null, effort: null, visibility: null };
            const activeEnabled = override.enabled !== null ? override.enabled : globalPolicy.enabled;
            const activeEffort = override.effort !== null ? override.effort : globalPolicy.effort;
            
            const isOverride = override.enabled !== null || override.effort !== null || override.visibility !== null;
            
            const effortMap = { low: '低', medium: '标准', high: '深度', custom: '自定义' };
            const effortText = effortMap[activeEffort] || '标准';

            badge.innerHTML = `<span>⚡ 推理: ${activeEnabled ? `开启 (${effortText})` : '关闭'}${isOverride ? ' <strong style="color:var(--chat-success, #10b981);">(覆写)</strong>' : ' (默认)'}</span>`;
            if (isOverride) {
                badge.style.borderColor = 'var(--chat-success, #10b981)';
                badge.style.background = 'rgba(16, 185, 129, 0.08)';
                badge.style.color = 'var(--chat-success, #10b981)';
            } else {
                badge.style.borderColor = 'rgba(99, 102, 241, 0.25)';
                badge.style.background = 'rgba(99, 102, 241, 0.08)';
                badge.style.color = '#818cf8';
            }
        }

        escapeHtml(text) {
            if (text == null) return '';
            return String(text).replace(/[&<>"']/g, m => ({
                '&': '&amp;',
                '<': '&lt;',
                '>': '&gt;',
                '"': '&quot;',
                "'": '&#039;'
            })[m]);
        }

        sanitizeRenderedHtml(html) {
            const tpl = document.createElement('template');
            tpl.innerHTML = html || '';
            tpl.content.querySelectorAll('script, iframe, object, embed, style, link').forEach(el => el.remove());
            tpl.content.querySelectorAll('*').forEach(el => {
                [...el.attributes].forEach(attr => {
                    const name = attr.name.toLowerCase();
                    const value = String(attr.value || '').trim().toLowerCase();
                    if (name.startsWith('on') || value.startsWith('javascript:')) {
                        el.removeAttribute(attr.name);
                    }
                });
            });
            return tpl.innerHTML;
        }

        async renderDocMarkdown(mdText, targetElement) {
            if (!targetElement) return;
            await window.loadLib('marked');
            const markedApi = window.marked && (window.marked.parse ? window.marked : window.marked.marked);
            if (!markedApi || typeof markedApi.parse !== 'function') {
                throw new Error('marked 解析器未正确加载');
            }

            const RendererCtor = markedApi.Renderer || (window.marked && window.marked.Renderer);
            if (!RendererCtor) {
                throw new Error('marked Renderer 不可用');
            }

            const renderer = new RendererCtor();
            const _self = this;
            renderer.code = function(tokenOrCode, infostring) {
                const token = tokenOrCode && typeof tokenOrCode === 'object' ? tokenOrCode : null;
                const code = token ? (token.text || '') : String(tokenOrCode || '');
                const lang = token ? (token.lang || '') : (infostring || '');
                const lowerLang = String(lang || '').toLowerCase().trim();
                if (lowerLang === 'mermaid') {
                    const escapedCode = encodeURIComponent(code.trim());
                    return `<div class="cogni-mermaid-wrapper" style="border:1px solid rgba(99,102,241,0.3); border-radius:8px; margin:16px 0; overflow:hidden;">
                        <div class="cogni-mermaid-header" style="display:flex; justify-content:space-between; align-items:center; padding:8px 12px; background:rgba(99,102,241,0.1); border-bottom:1px solid rgba(99,102,241,0.2);">
                            <span style="font-size:11px; color:#818cf8; font-weight:600;">📊 架构关系图谱 (Mermaid)</span>
                            <button class="cogni-mermaid-btn" onclick="SourceAstra.copyMermaidCode(this)" data-code="${escapedCode}" style="background:rgba(99,102,241,0.2); border:none; color:#a5b4fc; padding:4px 10px; border-radius:4px; font-size:10px; cursor:pointer;">📋 复制</button>
                        </div>
                        <div class="mermaid-container" style="padding:16px; background:rgba(0,0,0,0.2);">
                            <div class="mermaid">${_self.escapeHtml(code.trim())}</div>
                        </div>
                    </div>`;
                }
                return `<pre><code class="language-${_self.escapeHtml(lowerLang || 'text')}">${_self.escapeHtml(code)}</code></pre>`;
            };

            const rendered = markedApi.parse(mdText || '', { renderer, breaks: true, gfm: true });
            const html = rendered && typeof rendered.then === 'function' ? await rendered : rendered;
            targetElement.innerHTML = this.sanitizeRenderedHtml(html);

            // 动态注入代码块一键复制动作与语言标徽
            targetElement.querySelectorAll('pre').forEach(pre => {
                if (pre.querySelector('.explore-doc-code-actions')) return;
                const codeEl = pre.querySelector('code');
                let lang = 'text';
                if (codeEl) {
                    const classes = Array.from(codeEl.classList);
                    const langClass = classes.find(c => c.startsWith('language-'));
                    if (langClass) {
                        lang = langClass.replace('language-', '');
                    }
                }
                
                pre.style.position = 'relative';
                const actionsDiv = document.createElement('div');
                actionsDiv.className = 'explore-doc-code-actions';
                actionsDiv.innerHTML = `
                    <span style="font-size:10px; color:#64748b; font-family:sans-serif; text-transform:uppercase;">${lang}</span>
                    <button class="explore-doc-code-btn" type="button">
                        <span>📋</span> 复制
                    </button>
                    <span class="copy-tooltip">已复制</span>
                `;
                pre.appendChild(actionsDiv);

                const copyBtn = actionsDiv.querySelector('.explore-doc-code-btn');
                const tooltip = actionsDiv.querySelector('.copy-tooltip');
                if (copyBtn && codeEl) {
                    copyBtn.onclick = (e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        navigator.clipboard.writeText(codeEl.textContent).then(() => {
                            tooltip.classList.add('active');
                            copyBtn.innerHTML = '<span>✅</span> 已复制';
                            setTimeout(() => {
                                tooltip.classList.remove('active');
                                copyBtn.innerHTML = '<span>📋</span> 复制';
                            }, 2000);
                        }).catch(err => {
                            console.error('Failed to copy code block:', err);
                        });
                    };
                }
            });

            if (targetElement.querySelectorAll('.mermaid').length > 0) {
                if (typeof window.ensureMermaid !== 'function') {
                    return;
                }
                await window.ensureMermaid();
                setTimeout(() => {
                    try {
                        if (window.mermaid && typeof window.mermaid.init === 'function') {
                            window.mermaid.init(undefined, targetElement.querySelectorAll('.mermaid'));
                        }
                    } catch (e) {
                        console.error('Mermaid render error:', e);
                    }
                }, 50);
            }
        }

        async generateDoc() {
            if (this.docGenerating) return;
            this.docGenerating = true;

            try {
                const type = this.currentLevel === 'file' ? 'file' : (this.currentDir === '__project__' ? 'project' : 'module');
                const key = this.currentLevel === 'file' ? this.currentFile : this.currentDir;
                
                this.renderDocGeneratingState();

                let codeContext = '';
                if (type === 'file') {
                    const snippetRes = await fetch(`/api/snippet?file=${encodeURIComponent(key)}&count=100000`, {
                        headers: window.saAuthHeaders ? window.saAuthHeaders() : {}
                    });
                    if (!snippetRes.ok) {
                        throw new Error(`无法获取代码内容: HTTP ${snippetRes.status}`);
                    }
                    const snippetData = await snippetRes.json();
                    if (snippetData.snippet && Array.isArray(snippetData.snippet)) {
                        codeContext = snippetData.snippet.join('\n');
                    } else if (snippetData.content) {
                        codeContext = snippetData.content;
                    }
                }

                this.renderDocGeneratingState('正在呼叫大模型分析生成文档...');

                const activeModel = window.getScopedLlmModelSlot
                    ? window.getScopedLlmModelSlot('GLOBAL')
                    : (localStorage.getItem('G_PRIMARY_MODEL') || localStorage.getItem('G_ACTIVE_MODEL') || 'deepseek-v3');
                const configs = window.getLlmConfigs ? window.getLlmConfigs() : {};
                const conf = configs[activeModel] || { name: 'DeepSeek-V3', model: 'deepseek-chat', url: '', key: '', temp: '30' };
                const timeoutSeconds = window.getLlmTimeoutSeconds ? window.getLlmTimeoutSeconds(conf) : 300;
                const retryCount = window.getLlmRetryCount ? window.getLlmRetryCount(conf) : 1;

                let systemPrompt = '';
                let userPrompt = '';
                if (type === 'file') {
                    systemPrompt = `你是 SourceAstra 资深系统架构师。请为指定的文件生成专业的 Markdown 架构理解文档。\n要求结构清晰，以纯粹的中文专业词汇输出，严禁啰嗦。`;
                    userPrompt = `请为文件 \`${key}\` 生成【文件理解文档】。\n\n该文件的完整源码内容如下：\n\`\`\`\n${codeContext}\n\`\`\`\n\n请严格包含以下维度进行分析，并输出优雅 Markdown：\n1. **核心职责 (Core Responsibilities)**\n2. **结构设计与关键函数 (Structure & Keys)**\n3. **核心算法或控制流 (Core Algorithms)**\n4. **关键外部/内部依赖 (Dependencies)**`;
                } else if (type === 'project') {
                    systemPrompt = `你是 SourceAstra 资深系统架构师。请为整个项目生成专业的 Markdown 架构理解文档。`;
                    const dirsList = Object.values(this.dirMap || {}).map(d => `- 目录: \`${d.dirPath}\`，文件 ${Object.keys(d.files || {}).length} 个，函数 ${d.functions.length} 个`).join('\n');
                    userPrompt = `请为当前项目生成【项目理解文档】。\n\n目录概览如下：\n${dirsList}\n\n请严格包含以下维度：\n1. **项目整体职责**\n2. **目录结构与分层**\n3. **关键调用关系**\n4. **后续阅读建议**`;
                } else {
                    systemPrompt = `你是 SourceAstra 资深系统架构师。请为指定的模块生成专业的 Markdown 架构理解文档。\n要求分析全面、重点突出，以纯粹的中文专业词汇输出，严禁啰嗦。`;
                    const currentDirData = this.dirMap[key];
                    const files = currentDirData ? Object.values(currentDirData.files) : [];
                    const filesList = files.map(f => {
                        const funcs = (f.functions || []).map(fn => fn.name).slice(0, 10).join(', ');
                        return `- 文件: \`${f.filePath}\` (包含函数: ${funcs || '无'})`;
                    }).join('\n');
                    userPrompt = `请为模块 \`${key}\` 生成【模块架构理解文档】。\n\n该模块包含的文件及部分函数列表如下：\n${filesList}\n\n请严格包含以下维度进行分析，并输出优雅 Markdown：\n1. **设计意图与全局职责 (Design Intent & Global Responsibilities)**\n2. **模块内部分工与结构说明 (Internal Division & Architecture)**\n3. **关键数据流或协作流程 (Key Workflows & Collaborations)**\n4. **模块后续演进或改进建议 (Evolution & Recommendations)**`;
                }

                const response = await fetch('/api/chat/completion', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json', ...(window.saAuthHeaders ? window.saAuthHeaders() : {}) },
                    body: JSON.stringify({
                        active_model: activeModel,
                        usage_context: {
                            feature: 'explore-docs',
                            project_id: key
                        },
                        messages: [
                            { role: 'system', content: systemPrompt },
                            { role: 'user', content: userPrompt }
                        ],
                        temperature: parseFloat(conf.temp || '30') / 100,
                        timeout_seconds: timeoutSeconds,
                        reasoning_override: {
                            enabled: (window.SA_REASONING_OVERRIDES && window.SA_REASONING_OVERRIDES.explore && window.SA_REASONING_OVERRIDES.explore.enabled !== null) ? window.SA_REASONING_OVERRIDES.explore.enabled : undefined,
                            effort: (window.SA_REASONING_OVERRIDES && window.SA_REASONING_OVERRIDES.explore && window.SA_REASONING_OVERRIDES.explore.effort) || undefined,
                            visibility: (window.SA_REASONING_OVERRIDES && window.SA_REASONING_OVERRIDES.explore && window.SA_REASONING_OVERRIDES.explore.visibility) || undefined
                        }
                    })
                });

                if (!response.ok) {
                    const errText = await response.text().catch(() => '');
                    throw new Error(`LLM 响应错误 HTTP ${response.status}: ${errText.slice(0, 200)}`);
                }

                let generatedContent = '';
                this.lastReasoningSummary = null;
                if (window.saConsumeSSEStream) {
                    await window.saConsumeSSEStream(response, (event, data) => {
                        switch (event) {
                            case 'reasoning-summary':
                                try { const rs = JSON.parse(data); this.lastReasoningSummary = rs.summary || null; } catch (_) {}
                                break;
                            case 'chunk':
                                generatedContent += data.replace(/\x1E/g, '\n');
                                break;
                        }
                    });
                }
                if (!generatedContent) {
                    throw new Error('LLM 返回的响应格式非预期，未找到 answer。');
                }

                this.renderDocGeneratingState('正在将文档保存到本地持久化层...');

                const saveRes = await fetch('/api/documents/save', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'Authorization': window.saAuthHeaders ? window.saAuthHeaders()['Authorization'] : ''
                    },
                    body: JSON.stringify({
                        type: type,
                        key: key,
                        content: generatedContent
                    })
                });

                if (!saveRes.ok) {
                    throw new Error(`持久化失败 HTTP ${saveRes.status}`);
                }

                await this.loadDocHistoryAndContent();
                this.updateDocButtonState();

            } catch (err) {
                this.docContent.innerHTML = `
                    <div style="display:flex; flex-direction:column; align-items:center; justify-content:center; height:70%; text-align:center; padding:20px; gap:12px;">
                        <span style="font-size:32px;">❌</span>
                        <div style="font-size:14px; font-weight:600; color:#ef4444;">生成失败</div>
                        <div style="font-size:12px; color:${THEME.textDim}; max-width:260px; line-height:1.5;">
                            ${this.escapeHtml(err.message || String(err))}
                        </div>
                        <button type="button" class="explore-doc-btn" id="explore-doc-retry-btn" style="margin-top:12px;">
                            重试
                        </button>
                    </div>
                `;
            } finally {
                this.docGenerating = false;
            }
        }
    }

    // 挂载到全局
    window.SourceAstra = window.SourceAstra || {};

    window.SourceAstra.initExplore = function () {
        const container = document.getElementById('view-explore');
        if (!container) return;
        if (window._exploreView) {
            window._exploreView.rawData = null;
            window._exploreView.currentLevel = 'top';
            window._exploreView.currentDir = null;
            window._exploreView.currentFile = null;
            window._exploreView.currentFuncId = null;
            window._exploreView.breadcrumb = [];
            window._exploreView.dirMap = null;
            window._exploreView.fileMap = null;
        } else {
            window._exploreView = new ExploreView('view-explore');
        }
    };

    window.SourceAstra.exploreLoad = function () {
        if (window._exploreView) {
            window._exploreView.load();
        }
    };

    window.SourceAstra.onLocaleChange = function (loc) {
        if (window._exploreView && window._exploreView.rawData) {
            window._exploreView.updateSize();
            if (window._exploreView.currentLevel === 'top') {
                window._exploreView._drawOverview();
            } else if (window._exploreView.currentLevel === 'module') {
                window._exploreView._drawModule(window._exploreView.currentDir);
            } else if (window._exploreView.currentLevel === 'file') {
                window._exploreView._drawFile(window._exploreView.currentFile);
            } else if (window._exploreView.currentLevel === 'trace') {
                window._exploreView._drawTrace(window._exploreView.currentFuncId);
            }
            if (typeof window._exploreView._renderBreadcrumb === 'function') {
                window._exploreView._renderBreadcrumb();
            }
        }
    };

    window.addEventListener('sa-locale-changed', () => {
        if (window._exploreView && window._exploreView.rawData) {
            window._exploreView.updateSize();
            if (window._exploreView.currentLevel === 'top') {
                window._exploreView._drawOverview();
            } else if (window._exploreView.currentLevel === 'module') {
                window._exploreView._drawModule(window._exploreView.currentDir);
            } else if (window._exploreView.currentLevel === 'file') {
                window._exploreView._drawFile(window._exploreView.currentFile);
            } else if (window._exploreView.currentLevel === 'trace') {
                window._exploreView._drawTrace(window._exploreView.currentFuncId);
            }
            if (typeof window._exploreView._renderBreadcrumb === 'function') {
                window._exploreView._renderBreadcrumb();
            }
        }
    });

    window.addEventListener('sa-theme-changed', () => {
        if (window._exploreView && window._exploreView.rawData) {
            // Force SVG recreation by resetting cached dimensions
            window._exploreView.width = 0;
            window._exploreView.height = 0;
            window._exploreView.updateSize();
            if (window._exploreView.currentLevel === 'top') {
                window._exploreView._drawOverview();
            } else if (window._exploreView.currentLevel === 'module') {
                window._exploreView._drawModule(window._exploreView.currentDir);
            } else if (window._exploreView.currentLevel === 'file') {
                window._exploreView._drawFile(window._exploreView.currentFile);
            } else if (window._exploreView.currentLevel === 'trace') {
                window._exploreView._drawTrace(window._exploreView.currentFuncId);
            }
        }
    });

    // 自动初始化
    if (document.getElementById('view-explore')) {
        window.SourceAstra.initExplore();
    }
})();
