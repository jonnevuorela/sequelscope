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
	"path/filepath"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"go-dash.jonnevuorela.com/ui"
)

type application struct {
	db            *sql.DB
	templateCache map[string]*template.Template
}

type templateData struct {
	CurrentYear int
	Entry       *Entry
	Entries     []*Entry
}

type Entry struct {
	Id      int
	Title   string
	Content string
	Created time.Time
}

type User struct {
	Id      int
	Name    string
	Email   string
	Created time.Time
}

func main() {
	addr := flag.String("addr", ":4000", "HTTP network address")
	dsn := flag.String("dsn", "web:pass@/snippetbox?parseTime=true", "MySQL data source name")
	flag.Parse()

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
		templateCache: templateCache,
	}

	log.Printf("Starting server on http://localhost%s", *addr)
	err = http.ListenAndServe(*addr, app.routes())
	log.Fatal(err)
}

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()

	FS, err := fs.Sub(ui.Files, "static")
	if err != nil {
		log.Fatal(err)
	}

	fileServer := http.FileServer(http.FS(FS))

	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

	mux.HandleFunc("/", app.home)

	return mux
}

func (app *application) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	entries, err := app.getLatestEntries()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := &templateData{
		CurrentYear: time.Now().Year(),
		Entries:     entries,
	}

	app.render(w, http.StatusOK, "home.tmpl", data)
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

func (app *application) getLatestEntries() ([]*Entry, error) {
	stmt := `SELECT id, title, content, created FROM snippets ORDER BY created DESC LIMIT 10`
	rows, err := app.db.Query(stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*Entry
	for rows.Next() {
		s := &Entry{}
		err = rows.Scan(&s.Id, &s.Title, &s.Content, &s.Created)
		if err != nil {
			return nil, err
		}
		entries = append(entries, s)
	}
	return entries, nil
}

var functions = template.FuncMap{
	// Add any custom template functions here
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
