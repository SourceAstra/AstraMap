package astramap

import (
	"fmt"
	"github.com/jmoiron/sqlx"
)

const SchemaDDL = `
-- AstraMap 增强节点表 (扩展 symbols 表的语义)
CREATE TABLE IF NOT EXISTS astramap_nodes (
    id             TEXT PRIMARY KEY,
    kind           TEXT NOT NULL,        -- function/class/struct/interface/...
    name           TEXT NOT NULL,
    qualified_name TEXT NOT NULL,        -- 完全限定名
    file_path      TEXT NOT NULL,
    language       TEXT NOT NULL,
    start_line     INTEGER NOT NULL,
    end_line       INTEGER NOT NULL,
    start_column   INTEGER DEFAULT 0,
    end_column     INTEGER DEFAULT 0,
    signature      TEXT,                -- 函数签名
    docstring      TEXT,                -- 文档注释
    visibility     TEXT,                -- public/private/protected
    return_type    TEXT,                -- 返回类型
    is_exported    INTEGER DEFAULT 0,
    updated_at     INTEGER NOT NULL
);

-- AstraMap 增强边表 (扩展 edges 表的语义)
-- No FK constraints: edges reference synthetic IDs (file:*, import:*, route:*) not in astramap_nodes
CREATE TABLE IF NOT EXISTS astramap_edges (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    source     TEXT NOT NULL,
    target     TEXT NOT NULL,
    kind       TEXT NOT NULL,           -- calls/contains/imports/extends/...
    provenance TEXT DEFAULT 'scip',     -- scip/tree-sitter/heuristic
    line       INTEGER,
    col        INTEGER,
    metadata   TEXT                     -- JSON
);

-- 文件跟踪表
CREATE TABLE IF NOT EXISTS astramap_files (
    path         TEXT PRIMARY KEY,
    content_hash TEXT NOT NULL,
    language     TEXT NOT NULL,
    size         INTEGER NOT NULL,
    modified_at  INTEGER NOT NULL,
    indexed_at   INTEGER NOT NULL,
    node_count   INTEGER DEFAULT 0,
    errors       TEXT                   -- JSON array
);

-- 治理缺陷与裁决缓存表
CREATE TABLE IF NOT EXISTS astramap_verdicts (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    symbol_id      TEXT NOT NULL,
    has_active_defect INTEGER DEFAULT 0,
    stage          TEXT,
    decision       TEXT,
    rule_id        TEXT,
    description    TEXT,
    suggestion     TEXT,
    operator       TEXT,
    updated_at     INTEGER NOT NULL
);

-- FTS5 全文搜索索引
CREATE VIRTUAL TABLE IF NOT EXISTS astramap_fts USING fts5(
    id, name, qualified_name, docstring, signature,
    content='astramap_nodes',
    content_rowid='rowid'
);

-- 性能索引
CREATE INDEX IF NOT EXISTS idx_am_nodes_kind ON astramap_nodes(kind);
CREATE INDEX IF NOT EXISTS idx_am_nodes_name ON astramap_nodes(name);
CREATE INDEX IF NOT EXISTS idx_am_nodes_qname ON astramap_nodes(qualified_name);
CREATE INDEX IF NOT EXISTS idx_am_nodes_file ON astramap_nodes(file_path);
CREATE INDEX IF NOT EXISTS idx_am_nodes_lower_name ON astramap_nodes(lower(name));
CREATE INDEX IF NOT EXISTS idx_am_edges_source_kind ON astramap_edges(source, kind);
CREATE INDEX IF NOT EXISTS idx_am_edges_target_kind ON astramap_edges(target, kind);
CREATE INDEX IF NOT EXISTS idx_am_edges_kind ON astramap_edges(kind);
CREATE UNIQUE INDEX IF NOT EXISTS idx_am_edges_unique ON astramap_edges(source, target, kind, line);
CREATE INDEX IF NOT EXISTS idx_am_files_language ON astramap_files(language);
CREATE INDEX IF NOT EXISTS idx_am_verdicts_symbol ON astramap_verdicts(symbol_id);

-- FTS 同步触发器
CREATE TRIGGER IF NOT EXISTS am_fts_ai AFTER INSERT ON astramap_nodes BEGIN
    INSERT INTO astramap_fts(rowid, id, name, qualified_name, docstring, signature)
    VALUES (NEW.rowid, NEW.id, NEW.name, NEW.qualified_name, NEW.docstring, NEW.signature);
END;

CREATE TRIGGER IF NOT EXISTS am_fts_ad AFTER DELETE ON astramap_nodes BEGIN
    INSERT INTO astramap_fts(astramap_fts, rowid, id, name, qualified_name, docstring, signature)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.name, OLD.qualified_name, OLD.docstring, OLD.signature);
END;

CREATE TRIGGER IF NOT EXISTS am_fts_au AFTER UPDATE ON astramap_nodes BEGIN
    INSERT INTO astramap_fts(astramap_fts, rowid, id, name, qualified_name, docstring, signature)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.name, OLD.qualified_name, OLD.docstring, OLD.signature);
    INSERT INTO astramap_fts(rowid, id, name, qualified_name, docstring, signature)
    VALUES (NEW.rowid, NEW.id, NEW.name, NEW.qualified_name, NEW.docstring, NEW.signature);
END;
`

// InitAstraMapSchema 初始化数据库 Schema
func InitAstraMapSchema(db *sqlx.DB) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	_, err := db.Exec("PRAGMA foreign_keys = ON")
	if err != nil {
		return fmt.Errorf("PRAGMA foreign_keys failed: %w", err)
	}

	// 检查并清理 astramap_edges 中的重复数据，防止创建唯一索引时冲突
	var tableExists int
	_ = db.Get(&tableExists, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='astramap_edges'")
	if tableExists > 0 {
		_, _ = db.Exec(`
			DELETE FROM astramap_edges 
			WHERE rowid NOT IN (
				SELECT MIN(rowid) 
				FROM astramap_edges 
				GROUP BY source, target, kind, COALESCE(line, 0)
			)
		`)
	}

	_, err = db.Exec(SchemaDDL)
	return err
}
