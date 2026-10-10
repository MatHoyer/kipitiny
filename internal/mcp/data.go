package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Database contents: browsing (read), the console and credentials (admin).
// The core checks the scopes of the data browser itself.

type listDatabasesOut struct {
	Databases []core.PgDatabase `json:"databases"`
}

func (t *tools) listDatabases(ctx context.Context, _ *mcp.CallToolRequest, in serviceArg) (*mcp.CallToolResult, listDatabasesOut, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, listDatabasesOut{}, friendly(err)
	}
	dbs, err := t.c.PgDatabases(ctx, svc.ID)
	return nil, listDatabasesOut{Databases: dbs}, friendly(err)
}

type createDatabaseIn struct {
	Service    string `json:"service" jsonschema:"the PostgreSQL, MySQL, MariaDB or MongoDB service as project/service, or its ID"`
	Name       string `json:"name" jsonschema:"lowercase letters, digits and _, starting with a letter or _"`
	Collection string `json:"collection,omitempty" jsonschema:"MongoDB only, required: the new database's first collection (a database without one doesn't exist)"`
}

func (t *tools) createDatabase(ctx context.Context, _ *mcp.CallToolRequest, in createDatabaseIn) (*mcp.CallToolResult, core.PgDatabase, error) {
	db, err := mutate(ctx, t.c, store.ScopeAdmin, "create_database", in.Service+"/"+in.Name, func() (core.PgDatabase, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return core.PgDatabase{}, err
		}
		return t.c.CreatePgDatabase(ctx, svc.ID, in.Name, in.Collection)
	})
	return nil, db, err
}

type listTablesIn struct {
	Service  string `json:"service" jsonschema:"the PostgreSQL, MySQL, MariaDB or MongoDB service as project/service, or its ID"`
	Database string `json:"database,omitempty" jsonschema:"one of the instance's databases (see list_databases); default the service's own"`
}

type listTablesOut struct {
	Tables []core.PgTable `json:"tables"`
}

func (t *tools) listTables(ctx context.Context, _ *mcp.CallToolRequest, in listTablesIn) (*mcp.CallToolResult, listTablesOut, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, listTablesOut{}, friendly(err)
	}
	ts, err := t.c.PgTables(ctx, svc.ID, in.Database)
	return nil, listTablesOut{Tables: ts}, friendly(err)
}

type readTableIn struct {
	Service  string          `json:"service" jsonschema:"the PostgreSQL, MySQL or MariaDB service as project/service, or its ID"`
	Database string          `json:"database,omitempty" jsonschema:"one of the instance's databases; default the service's own"`
	Schema   string          `json:"schema,omitempty" jsonschema:"PostgreSQL: default public; MySQL and MariaDB: the database"`
	Table    string          `json:"table" jsonschema:"a table or view (see list_tables)"`
	Limit    int             `json:"limit,omitempty" jsonschema:"rows to return, default 50, max 200"`
	Offset   int             `json:"offset,omitempty"`
	OrderBy  string          `json:"order_by,omitempty" jsonschema:"column to sort by"`
	Desc     bool            `json:"desc,omitempty" jsonschema:"sort descending"`
	Search   string          `json:"search,omitempty" jsonschema:"keep rows where any column contains this text (case-insensitive)"`
	Filters  []core.PgFilter `json:"filters,omitempty" jsonschema:"conditions all rows meet; op is one of = != < <= > >= like null notnull"`
}

func (t *tools) readTable(ctx context.Context, _ *mcp.CallToolRequest, in readTableIn) (*mcp.CallToolResult, core.PgRows, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, core.PgRows{}, friendly(err)
	}
	schema := in.Schema
	if schema == "" && svc.Kind == store.ServiceKindPostgres {
		schema = "public"
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := t.c.PgRows(ctx, svc.ID, in.Database, schema, in.Table, core.RowQuery{
		Limit: min(limit, core.DataPageMax), Offset: in.Offset, OrderBy: in.OrderBy, Desc: in.Desc,
		Search: in.Search, Filters: in.Filters,
	})
	return nil, rows, friendly(err)
}

type findDocumentsIn struct {
	Service    string `json:"service" jsonschema:"the MongoDB service as project/service, or its ID"`
	Database   string `json:"database,omitempty" jsonschema:"one of the instance's databases (see list_databases); default the service's own"`
	Collection string `json:"collection" jsonschema:"a collection or view (see list_tables)"`
	Filter     string `json:"filter,omitempty" jsonschema:"a query as Extended JSON, e.g. {\"status\": \"active\", \"age\": {\"$gte\": 18}}"`
	Sort       string `json:"sort,omitempty" jsonschema:"a sort as Extended JSON, e.g. {\"createdAt\": -1}"`
	Limit      int    `json:"limit,omitempty" jsonschema:"documents to return, default 50, max 200"`
	Skip       int    `json:"skip,omitempty"`
}

func (t *tools) findDocuments(ctx context.Context, _ *mcp.CallToolRequest, in findDocumentsIn) (*mcp.CallToolResult, core.MongoDocs, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, core.MongoDocs{}, friendly(err)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	docs, err := t.c.MongoDocuments(ctx, svc.ID, in.Database, in.Collection, core.DocQuery{
		Filter: in.Filter, Sort: in.Sort, Limit: min(limit, core.DataPageMax), Skip: in.Skip,
	})
	return nil, docs, friendly(err)
}

type scanRedisKeysIn struct {
	Service string `json:"service" jsonschema:"the Redis service as project/service, or its ID"`
	Pattern string `json:"pattern,omitempty" jsonschema:"glob the keys match, default *"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"the cursor a previous call returned, to continue; 0 when the scan is over"`
	Count   int    `json:"count,omitempty" jsonschema:"keys to look at in this step, max 200"`
}

func (t *tools) scanRedisKeys(ctx context.Context, _ *mcp.CallToolRequest, in scanRedisKeysIn) (*mcp.CallToolResult, core.RedisKeys, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, core.RedisKeys{}, friendly(err)
	}
	keys, err := t.c.RedisScan(ctx, svc.ID, in.Cursor, in.Pattern, in.Count)
	return nil, keys, friendly(err)
}

type getRedisKeyIn struct {
	Service string `json:"service" jsonschema:"the Redis service as project/service, or its ID"`
	Key     string `json:"key"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"the cursor a previous call returned, for the next page of a long list, set, hash or stream"`
	Count   int    `json:"count,omitempty" jsonschema:"items per page, max 200"`
}

func (t *tools) getRedisKey(ctx context.Context, _ *mcp.CallToolRequest, in getRedisKeyIn) (*mcp.CallToolResult, core.RedisValue, error) {
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, core.RedisValue{}, friendly(err)
	}
	v, err := t.c.RedisGet(ctx, svc.ID, in.Key, in.Cursor, in.Count)
	return nil, v, friendly(err)
}

type queryDatabaseIn struct {
	Service  string `json:"service" jsonschema:"the database service as project/service, or its ID"`
	Query    string `json:"query" jsonschema:"SQL for PostgreSQL, MySQL and MariaDB (only the last statement's rows are returned), a redis-cli command for Redis, mongosh JavaScript for MongoDB (its printout is returned)"`
	Database string `json:"database,omitempty" jsonschema:"one of the instance's databases (all but Redis); default the service's own"`
	Write    bool   `json:"write,omitempty" jsonschema:"allow changes; without it PostgreSQL runs read-only, MySQL, MariaDB and MongoDB run as a read-only user, and Redis refuses commands that write. With it, PostgreSQL runs the query in one transaction"`
}

// queryDatabase is audited by the core, without the query's text.
func (t *tools) queryDatabase(ctx context.Context, _ *mcp.CallToolRequest, in queryDatabaseIn) (*mcp.CallToolResult, core.ConsoleResult, error) {
	if err := core.Require(ctx, store.ScopeAdmin); err != nil {
		return nil, core.ConsoleResult{}, err
	}
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, core.ConsoleResult{}, friendly(err)
	}
	res, err := t.c.DataConsole(ctx, svc.ID, in.Database, in.Query, in.Write)
	return nil, res, friendly(err)
}

// getConnection reveals credentials: admin only, like the REST route.
func (t *tools) getConnection(ctx context.Context, _ *mcp.CallToolRequest, in serviceArg) (*mcp.CallToolResult, core.Connection, error) {
	if err := core.Require(ctx, store.ScopeAdmin); err != nil {
		return nil, core.Connection{}, err
	}
	svc, err := t.c.ResolveService(ctx, in.Service)
	if err != nil {
		return nil, core.Connection{}, friendly(err)
	}
	conn, err := t.c.DatabaseConnection(ctx, svc.ID)
	return nil, conn, friendly(err)
}
