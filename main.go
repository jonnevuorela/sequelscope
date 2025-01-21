package main

import (
	"bytes"
	"database/sql"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"github.com/justinas/alice"
	"go-dash.jonnevuorela.com/ui"
)

type application struct {
	errorLog       *log.Logger
	infoLog        *log.Logger
	db             *sql.DB
	entries        []*Entry
	templateCache  map[string]*template.Template
	binlogSyncer   *replication.BinlogSyncer
	binlogStreamer *replication.BinlogStreamer
}
type templateData struct {
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

func main() {
	err := godotenv.Load(".env")
	addr := flag.String("addr", ":4000", "HTTP network address")
	dsn := flag.String("dsn", os.Getenv("WEB_DSN"), "MySQL data source name")
	flag.Parse()

	infoLog := log.New(os.Stdout, "\033[42;30mINFO\033[0m\t", log.Ldate|log.Ltime)
	errorLog := log.New(os.Stderr, "\033[41;30mERROR\033[0m\t", log.Ldate|log.Ltime|log.Lshortfile)

	if err != nil {
		log.Fatal(err)
	}

	db, err := sql.Open("mysql", *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatal(err)
	}

	templateCache, err := newTemplateCache()
	if err != nil {
		log.Fatal(err)
	}

	app := &application{
		db:            db,
		entries:       []*Entry{},
		errorLog:      errorLog,
		infoLog:       infoLog,
		templateCache: templateCache,
	}

	app.getDatabases()

	log.Printf("Starting server on http://localhost%s", *addr)
	err = http.ListenAndServe(*addr, app.routes())
	log.Fatal(err)
}
func (app *application) routes() http.Handler {
	mux := http.NewServeMux()

	// Static files
	FS, err := fs.Sub(ui.Files, "static")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(FS))
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

	// Application routes
	mux.HandleFunc("/", app.home)
	mux.HandleFunc("/entry/view/", app.dbTitleView)
	mux.HandleFunc("/entry/view/table", app.tableView)

	standard := alice.New(app.recoverPanic, app.logRequest)
	return standard.Then(mux)
}

func (app *application) setupBinlogWatcher() error {
	cfg := replication.BinlogSyncerConfig{
		ServerID: 100,
	}

}

func (app *application) home(w http.ResponseWriter, r *http.Request) {
	if len(app.entries) == 0 {
		err := app.getDatabases()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	data := &templateData{
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

	var tableData TableData
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
	data.Entry = &Entry{
		Title: dbName,
		Tables: []Table{
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
	entry.Tables = []Table{}

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

		var columns []Column
		for colRows.Next() {
			var col Column
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
		var latest LatestRow
		var titleBytes []byte
		stmt = fmt.Sprintf("SELECT id, title FROM %s.%s ORDER BY id DESC LIMIT 1", entry.Title, tableName)
		app.db.QueryRow(stmt).Scan(&latest.Id, &titleBytes)

		latest.Title = string(titleBytes)

		table := Table{
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

func (app *application) render(w http.ResponseWriter, status int, page string, data *templateData) {
	ts, ok := app.templateCache[page]
	if !ok {
		err := fmt.Errorf("the template %s does not exist", page)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	buf := new(bytes.Buffer)
	err := ts.ExecuteTemplate(buf, "base", data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (app *application) getDatabases() error {
	id := 0

	rows, err := app.db.Query("SHOW DATABASES")
	if err != nil {
		log.Fatal(err)
	}

	defer rows.Close()

	for rows.Next() {
		var dbName string
		if err := rows.Scan(&dbName); err != nil {
			log.Fatal(err)
		}
		db := &Entry{
			Title: dbName,
			Id:    id,
		}
		app.entries = append(app.entries, db)
		id++
	}

	if err := rows.Err(); err != nil {
		log.Fatal(err)
		return err
	}

	return nil

}

var functions = template.FuncMap{
	"truncate": func(s string, length int) string {
		if len(s) <= length {
			return s
		}
		return s[:length] + "..."
	},
}

func (app *application) newTemplateData(r *http.Request) *templateData {
	return &templateData{}
}
func newTemplateCache() (map[string]*template.Template, error) {
	cache := map[string]*template.Template{}

	pages, err := fs.Glob(ui.Files, "html/pages/*.tmpl")
	if err != nil {
		return nil, err
	}

	for _, page := range pages {
		name := filepath.Base(page)

		patterns := []string{
			"html/base.tmpl",
			"html/partials/*.tmpl",
			page,
		}

		ts, err := template.New(name).Funcs(functions).ParseFS(ui.Files, patterns...)
		if err != nil {
			return nil, err
		}

		cache[name] = ts
	}

	return cache, nil
}

func (app *application) serverError(w http.ResponseWriter, err error) {
	trace := fmt.Sprintf("%s\n%s", err.Error(), debug.Stack())
	app.errorLog.Output(2, trace)

	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

func (app *application) clientError(w http.ResponseWriter, status int) {
	http.Error(w, http.StatusText(status), status)
}

func (app *application) notFound(w http.ResponseWriter) {
	app.clientError(w, http.StatusNotFound)
}

func (app *application) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.infoLog.Printf("%s - %s %s %s", r.RemoteAddr, r.Proto, r.Method, r.URL.RequestURI())

		next.ServeHTTP(w, r)
	})
}

func (app *application) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				w.Header().Set("Connection", "close")
				app.serverError(w, fmt.Errorf("%s", err))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
