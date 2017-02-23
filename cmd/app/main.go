package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	conf "github.com/mgutz/configpipe"
	"github.com/mgutz/sshtunnel"

	"gopkg.in/mgutz/dat.v2/dat"
	runner "gopkg.in/mgutz/dat.v2/sqlx-runner"

	"golang.org/x/crypto/ssh"
)

var config *conf.Configuration

func init() {
	var err error
	config, err = conf.Runv(
		conf.YAMLFile(&conf.File{Path: "./config.yaml"}),
		conf.Argv(),
		//conf.Trace(),
	)
	if err != nil {
		panic(err)
	}
}

// open connection to postgres
func postgres(connstr string) (*runner.DB, error) {
	sqlDB, err := sql.Open("postgres", connstr)
	if err != nil {
		return nil, err
	}

	runner.MustPing(sqlDB)

	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetMaxOpenConns(2)

	dat.Strict = true
	dat.EnableInterpolation = true
	runner.LogQueriesThreshold = 20 * time.Millisecond
	runner.LogErrNoRows = false

	return runner.NewDB(sqlDB, "postgres"), nil
}

// Open database connection using environment variable credentials.
func openDatabase(user string, password string) (*runner.DB, error) {
	connstr := fmt.Sprintf("user=%s password=%s dbname=mno_production host=127.0.0.1 port=25432 sslmode=disable", user, password)
	return postgres(connstr)
}

// Get the count of users from production database.
func query(conn runner.Connection) error {
	var count int
	conn.SQL(`select count(*) from users`).QueryScalar(&count)
	fmt.Println("COUNT", count)
	return nil
}

func openTunnel() (*sshtunnel.SSHTunnel, error) {
	sshConfig := &ssh.ClientConfig{
		User: config.MustString("ssh.user"),
		Auth: []ssh.AuthMethod{
			sshtunnel.SSHAgent(),
		},
	}

	tunnelConf := sshtunnel.Config{
		SSHAddress:    config.MustString("ssh.address"),
		RemoteAddress: "127.0.0.1:5432",
		LocalAddress:  "127.0.0.1:25432",
		SSHConfig:     sshConfig,
	}

	tunnel := sshtunnel.New(&tunnelConf)

	if err := <-tunnel.Open(); err != nil {
		tunnel.Close()
		return nil, err
	}

	return tunnel, nil
}

func main() {
	tunnel, err := openTunnel()
	if err != nil {
		panic(err)
	}

	db, err := openDatabase(config.MustString("pg.user"), config.MustString("pg.password"))
	if err != nil {
		panic(err)
	}

	cleanup := func() {
		if tunnel != nil {
			tunnel.Close()
		}
	}
	defer cleanup()

	// TODO is there a more elegant way to autoclose in tunnel itself if the program
	// is terminated
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cleanup()
		os.Exit(1)
	}()

	err = query(db)
	if err != nil {
		fmt.Println(err)
	}
}
