package types

import (
	"database/sql"
	"time"
)

type TemplateData struct {
	CurrentYear int
	Entry       *Entry
	Entries     []*Entry
	TableData   *TableData
}

type Column struct {
	Field   string
	Type    string
	Null    string
	Key     string
	Default sql.NullString
	Extra   string
}

type Table struct {
	TableName   string
	Columns     []Column
	EntryCount  int
	LatestEntry LatestRow
}

type TableData struct {
	Columns []string
	Rows    []map[string]string
}

type Entry struct {
	Id      int
	Title   string
	Tables  []Table
	Created time.Time
}

type User struct {
	Id      int
	Name    string
	Email   string
	Created time.Time
}

type LatestRow struct {
	Id    int
	Title string
}
