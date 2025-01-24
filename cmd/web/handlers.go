package main

import (
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/justinas/alice"
	"sequelscope.jonnevuorela.com/types"
	"sequelscope.jonnevuorela.com/ui"
)

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()

	FS, err := fs.Sub(ui.Files, "static")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(FS))
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

	mux.HandleFunc("/ws", app.handleWebSocket)

	mux.HandleFunc("/", app.home)
	mux.HandleFunc("/entry/view/", app.dbTitleView)
	mux.HandleFunc("/entry/view/table", app.tableView)

	standard := alice.New(app.recoverPanic, app.logRequest)
	return standard.Then(mux)
}
func (app *application) home(w http.ResponseWriter, r *http.Request) {
	if len(app.entries) == 0 {
		err := app.getDatabases()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	data := &types.TemplateData{
		CurrentYear: time.Now().Year(),
		Entries:     app.entries,
	}
	app.render(w, http.StatusOK, "home.tmpl", data)
}
func (app *application) tableView(w http.ResponseWriter, r *http.Request) {
	dbName := r.URL.Query().Get("db")
	tableName := r.URL.Query().Get("table")

	if dbName == "" || tableName == "" {
		app.notFound(w)
		return
	}

	stmt := fmt.Sprintf("SELECT * FROM %s.%s LIMIT 100", dbName, tableName)
	rows, err := app.db.Query(stmt)
	if err != nil {
		app.serverError(w, err)
		return
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		app.serverError(w, err)
		return
	}

	// Prepare data container
	values := make([]sql.RawBytes, len(columns))
	scanArgs := make([]interface{}, len(values))
	for i := range values {
		scanArgs[i] = &values[i]
	}

	var tableData types.TableData
	tableData.Columns = columns

	for rows.Next() {
		err = rows.Scan(scanArgs...)
		if err != nil {
			app.serverError(w, err)
			return
		}

		row := make(map[string]string)
		for i, col := range values {
			if col == nil {
				row[columns[i]] = "NULL"
			} else {
				row[columns[i]] = string(col)
			}
		}
		tableData.Rows = append(tableData.Rows, row)
	}

	if err = rows.Err(); err != nil {
		app.serverError(w, err)
		return
	}

	data := app.newTemplateData(r)
	data.Entry = &types.Entry{
		Title: dbName,
		Tables: []types.Table{
			{
				TableName: tableName,
			},
		},
	}
	data.TableData = &tableData

	app.render(w, http.StatusOK, "table.tmpl", data)
}
func (app *application) dbTitleView(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimPrefix(request.URL.Path, "/entry/view/")
	idNum, err := strconv.Atoi(id)
	if err != nil {
		app.notFound(writer)
		return
	}

	if idNum >= len(app.entries) {
		app.notFound(writer)
		return
	}

	entry := app.entries[idNum]
	entry.Tables = []types.Table{}

	stmt := "SHOW TABLES FROM " + entry.Title
	rows, err := app.db.Query(stmt)
	if err != nil {
		app.serverError(writer, err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			app.serverError(writer, err)
			return
		}

		// Get columns for each table
		stmt := fmt.Sprintf("SHOW COLUMNS FROM %s.%s", entry.Title, tableName)
		colRows, err := app.db.Query(stmt)
		if err != nil {
			app.serverError(writer, err)
			return
		}
		defer colRows.Close()

		var columns []types.Column
		for colRows.Next() {
			var col types.Column
			if err := colRows.Scan(
				&col.Field,
				&col.Type,
				&col.Null,
				&col.Key,
				&col.Default,
				&col.Extra,
			); err != nil {
				app.serverError(writer, err)
				return
			}
			columns = append(columns, col)
		}

		// Get count of rows
		stmt = fmt.Sprintf("SELECT COUNT(*) FROM %s.%s", entry.Title, tableName)
		var count int
		if err := app.db.QueryRow(stmt).Scan(&count); err != nil {
			app.serverError(writer, err)
			return
		}

		// Get latest entry
		var latest types.LatestRow
		var titleBytes []byte
		stmt = fmt.Sprintf("SELECT id, title FROM %s.%s ORDER BY id DESC LIMIT 1", entry.Title, tableName)
		app.db.QueryRow(stmt).Scan(&latest.Id, &titleBytes)

		latest.Title = string(titleBytes)

		table := types.Table{
			TableName:   tableName,
			Columns:     columns,
			EntryCount:  count,
			LatestEntry: latest,
		}
		entry.Tables = append(entry.Tables, table)
	}

	data := app.newTemplateData(request)
	data.Entry = entry
	app.render(writer, http.StatusOK, "view.tmpl", data)
}

func (app *application) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		app.errorLog.Printf("Websocket upgrade failed: %v", err)
		return
	}

	app.clientsMux.Lock()
	app.clients[conn] = true
	app.clientsMux.Unlock()

	defer func() {
		conn.Close()
		app.clientsMux.Lock()
		delete(app.clients, conn)
		app.clientsMux.Unlock()
	}()

	// keeping connection
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}
