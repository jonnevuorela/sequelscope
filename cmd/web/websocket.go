package main

import (
	"context"
	"database/sql"
	"os"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	mysqlDriver "github.com/go-sql-driver/mysql"
)

func (app *application) setupBinlogWatcher() {
	dsn, err := mysqlDriver.ParseDSN(os.Getenv("REPL_DSN"))
	if err != nil {
		app.errorLog.Printf("error parsing DSN: %v", err)
		return
	}

	testDb, err := sql.Open("mysql", os.Getenv("REPL_DSN"))
	if err != nil {
		app.errorLog.Printf("Test connection failed: %v", err)
		return
	}
	defer testDb.Close()

	err = testDb.Ping()
	if err != nil {
		app.errorLog.Printf("Test ping failed: %v", err)
		return
	}
	var (
		file            string
		pos             uint32
		binlogDoDB      sql.NullString
		binlogIgnoreDB  sql.NullString
		executedGtidSet sql.NullString
	)
	err = testDb.QueryRow("SHOW MASTER STATUS").Scan(
		&file,
		&pos,
		&binlogDoDB,
		&binlogIgnoreDB,
		&executedGtidSet,
	)
	if err != nil {
		app.errorLog.Printf("Direct SHOW MASTER STATUS failed: %v", err)
		return
	}

	syncerConfig := replication.BinlogSyncerConfig{
		ServerID: 100,
		Flavor:   "mysql",
		Host:     "localhost",
		Port:     3306,
		User:     dsn.User,
		Password: dsn.Passwd,
	}

	app.binlogSyncer = replication.NewBinlogSyncer(syncerConfig)

	streamer, err := app.binlogSyncer.StartSync(mysql.Position{Name: file, Pos: pos})
	if err != nil {
		app.errorLog.Printf("error starting binlog sync: %v", err)
		return
	}

	app.binlogStreamer = streamer
	app.infoLog.Printf("Binlog setup complete")

	go func() {
		for {
			ev, err := app.binlogStreamer.GetEvent(context.Background())
			if err != nil {
				app.errorLog.Printf("Binlog event error: %v", err)
				continue
			}
			switch e := ev.Event.(type) {
			case *replication.RowsEvent:
				app.handleRowsEvent(e)
			case *replication.QueryEvent:
				app.handleQueryEvent(e)
			}
		}
	}()
}

func (app *application) handleRowsEvent(e *replication.RowsEvent) {
	message := map[string]string{
		"type":     "row_change",
		"table":    string(e.Table.Table),
		"database": string(e.Table.Schema),
	}
	app.broadcastChange(message)
}

func (app *application) handleQueryEvent(e *replication.QueryEvent) {
	message := map[string]string{
		"type":     "query",
		"database": string(e.Schema),
		"query":    string(e.Query),
	}
	app.broadcastChange(message)
}

func (app *application) broadcastChange(message map[string]string) {
	app.clientsMux.RLock()
	defer app.clientsMux.RUnlock()

	for client := range app.clients {
		err := client.WriteJSON(message)
		if err != nil {
			app.errorLog.Printf("Error broadcasting to client: %v", err)
			client.Close()
			delete(app.clients, client)
		}
	}
}
