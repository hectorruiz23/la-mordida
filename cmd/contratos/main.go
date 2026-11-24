// Command contratos is the single binary of La Mordida: it imports public
// procurement publications and serves the readings over what it stored.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	consultashttp "github.com/hectorruiz23/la-mordida/internal/consultas/adapters/http"
	consultassqlite "github.com/hectorruiz23/la-mordida/internal/consultas/adapters/sqlite"
	consultasapp "github.com/hectorruiz23/la-mordida/internal/consultas/app"
	contratossqlite "github.com/hectorruiz23/la-mordida/internal/contratos/adapters/sqlite"
	contratosapp "github.com/hectorruiz23/la-mordida/internal/contratos/app"
	fixture "github.com/hectorruiz23/la-mordida/internal/ingesta/adapters/fixture"
	ingestaapp "github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
	"github.com/hectorruiz23/la-mordida/internal/platform/httpserver"
	"github.com/hectorruiz23/la-mordida/migrations"
)

const usage = `La Mordida — contratación pública, sin ruido.

Usage:
  contratos import [flags]   importa publicaciones de una fuente
  contratos serve [flags]    sirve las consultas sobre los datos guardados
  contratos version          imprime la versión

Opciones de import:
  -db string        fichero SQLite (por defecto contratos.db)
  -fixture string   fixture neutral local (por defecto testdata/neutral/sample-award.json)
  -from string      solo publicaciones actualizadas desde este instante RFC 3339
  -to string        solo publicaciones actualizadas hasta este instante RFC 3339

Opciones de serve:
  -db string        fichero SQLite (por defecto contratos.db)
  -addr string      dirección de escucha (por defecto 127.0.0.1:8770)`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "contratos: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return errors.New("falta un comando")
	}

	switch args[0] {
	case "-h", "--help", "help":
		fmt.Println(usage)
		return nil
	case "version", "--version":
		fmt.Println("dev")
		return nil
	case "import":
		return runImport(args[1:])
	case "serve":
		return runServe(args[1:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		return fmt.Errorf("comando desconocido %q", args[0])
	}
}

func runImport(args []string) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	dbPath := flags.String("db", envOr("CONTRATOS_DB", "contratos.db"), "fichero SQLite")
	fixturePath := flags.String("fixture", envOr("CONTRATOS_FIXTURE", "testdata/neutral/sample-award.json"), "fixture neutral local")
	from := flags.String("from", "", "solo publicaciones actualizadas desde este instante RFC 3339")
	to := flags.String("to", "", "solo publicaciones actualizadas hasta este instante RFC 3339")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("import: argumentos inesperados %v", flags.Args())
	}

	window, err := parseWindow(*from, *to)
	if err != nil {
		return err
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.Migrate(ctx, database, migrations.FS); err != nil {
		return err
	}

	store := contratossqlite.New(database)
	importer := contratosapp.NewImporter(store)
	reader := fixture.New(*fixturePath)
	stored, err := ingestaapp.NewImporter(reader, importer).Run(ctx, window)
	if err != nil {
		return err
	}

	fmt.Printf("import: %d publicaciones guardadas en %s\n", stored, *dbPath)
	return nil
}

func runServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbPath := flags.String("db", envOr("CONTRATOS_DB", "contratos.db"), "fichero SQLite")
	addr := flags.String("addr", envOr("CONTRATOS_ADDR", "127.0.0.1:8770"), "dirección de escucha")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("serve: argumentos inesperados %v", flags.Args())
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.Migrate(ctx, database, migrations.FS); err != nil {
		return err
	}

	store := consultassqlite.New(database)
	handler := consultashttp.New(consultasapp.New(store))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", httpserver.Health(database))
	mux.HandleFunc("GET /api/expedientes", handler.List)

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("escuchar en %s: %w", *addr, err)
	}
	defer listener.Close()

	fmt.Printf("serve: escuchando en %s sobre %s\n", listener.Addr(), *dbPath)

	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := httpserver.New(*addr, mux)
	shutdown := make(chan struct{})
	go func() {
		defer close(shutdown)
		<-signals.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	<-shutdown
	return nil
}

func parseWindow(from, to string) (ingestaapp.Window, error) {
	var window ingestaapp.Window
	var err error
	if from != "" {
		window.From, err = time.Parse(time.RFC3339, from)
		if err != nil {
			return ingestaapp.Window{}, fmt.Errorf("import: -from: %w", err)
		}
	}
	if to != "" {
		window.To, err = time.Parse(time.RFC3339, to)
		if err != nil {
			return ingestaapp.Window{}, fmt.Errorf("import: -to: %w", err)
		}
	}
	if !window.From.IsZero() && !window.To.IsZero() && window.To.Before(window.From) {
		return ingestaapp.Window{}, errors.New("import: -to es anterior a -from")
	}
	return window, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
