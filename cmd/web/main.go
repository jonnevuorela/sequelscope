package main

import (
	"database/sql"
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"

	"sequelscope.jonnevuorela.com/types"
)

type application struct {
	errorLog      *log.Logger
	infoLog       *log.Logger
	db            *sql.DB
	entries       []*types.Entry
	templateCache map[string]*template.Template

	binlogSyncer   *replication.BinlogSyncer
	binlogStreamer *replication.BinlogStreamer
	clients        map[*websocket.Conn]bool
	clientsMux     sync.RWMutex
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	err := godotenv.Load(".env")
	addr := flag.String("addr", ":4001", "HTTP network address")
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
		entries:       []*types.Entry{},
		errorLog:      errorLog,
		infoLog:       infoLog,
		templateCache: templateCache,
		clients:       make(map[*websocket.Conn]bool),
	}
	app.getDatabases()

	app.setupBinlogWatcher()

	defer app.binlogSyncer.Close()

	log.Printf("Starting server on http://localhost%s", *addr)
	err = http.ListenAndServe(*addr, app.routes())
	log.Fatal(err)
}
