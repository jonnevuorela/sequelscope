package main

import (
	"database/sql"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/gorilla/websocket"

	"sequelscope.jonnevuorela.com/types"
)

type application struct {
	errorLog      *log.Logger
	infoLog       *log.Logger
	db            *sql.DB
	dsn           string
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
	addr := flag.String("addr", ":4001", "HTTP network address")
	dsn := flag.String("dsn", formDsn(), "MySQL data source name")

	flag.Parse()

	infoLog := log.New(os.Stdout, "\033[42;30mINFO\033[0m\t", log.Ldate|log.Ltime)
	errorLog := log.New(os.Stderr, "\033[41;30mERROR\033[0m\t", log.Ldate|log.Ltime|log.Lshortfile)

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
		dsn:           *dsn,
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

func formDsn() string {

	println("Enter username for database: ")
	var user string
	_, err := fmt.Scan(&user)

	println("Enter password for the username: ")
	var password string
	_, err = fmt.Scan(&password)

	println("Enter database port: ")
	var port string
	_, err = fmt.Scan(&port)

	println("Enter database name: ")
	var dbname string
	_, err = fmt.Scan(&dbname)

	if err != nil {

		fmt.Errorf("dsn formatting failed: %v", err)
	}

	dsn := fmt.Sprintf("%v:%v@tcp(127.0.0.1:%v)/%v?parseTime=true", user, password, port, dbname)
	fmt.Print(dsn)
	return dsn
}
