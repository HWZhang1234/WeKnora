package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/duckdb/duckdb-go/v2"
)

// duckdbExtensions is the list of DuckDB extensions required by WeKnora's
// data analysis tool. `spatial` is used for layer metadata (st_read_meta)
// so we can enumerate sheet names from Excel files, while `excel` provides
// the dedicated read_xlsx reader with proper type inference.
var duckdbExtensions = []string{"spatial", "excel"}

func downloadExtensions() {
	ctx := context.Background()

	sqlDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		panic(err)
	}
	defer sqlDB.Close()

	// If HTTPS_PROXY or HTTP_PROXY is set, configure DuckDB's internal HTTP
	// client to use it (DuckDB does not read standard env vars).
	if proxy := getProxy(); proxy != "" {
		if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("SET http_proxy='%s';", proxy)); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: failed to set DuckDB http_proxy: %v\n", err)
		}
	}

	for _, ext := range duckdbExtensions {
		if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("INSTALL %s;", ext)); err != nil {
			panic(fmt.Errorf("failed to install %s extension: %w", ext, err))
		}
		if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("LOAD %s;", ext)); err != nil {
			panic(fmt.Errorf("failed to load %s extension: %w", ext, err))
		}
	}
}

func getProxy() string {
	if p := os.Getenv("HTTPS_PROXY"); p != "" {
		return p
	}
	return os.Getenv("HTTP_PROXY")
}

func main() {
	downloadExtensions()
}
