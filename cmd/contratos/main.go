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

	archivodisk "github.com/hectorruiz23/la-mordida/internal/archivo/adapters/disk"
	archivohttp "github.com/hectorruiz23/la-mordida/internal/archivo/adapters/http"
	archivosqlite "github.com/hectorruiz23/la-mordida/internal/archivo/adapters/sqlite"
	archivoapp "github.com/hectorruiz23/la-mordida/internal/archivo/app"
	consultashttp "github.com/hectorruiz23/la-mordida/internal/consultas/adapters/http"
	consultassqlite "github.com/hectorruiz23/la-mordida/internal/consultas/adapters/sqlite"
	consultasapp "github.com/hectorruiz23/la-mordida/internal/consultas/app"
	contratossqlite "github.com/hectorruiz23/la-mordida/internal/contratos/adapters/sqlite"
	fixture "github.com/hectorruiz23/la-mordida/internal/ingesta/adapters/fixture"
	"github.com/hectorruiz23/la-mordida/internal/ingesta/adapters/placsp"
	ingestasqlite "github.com/hectorruiz23/la-mordida/internal/ingesta/adapters/sqlite"
	ingestaapp "github.com/hectorruiz23/la-mordida/internal/ingesta/app"
	"github.com/hectorruiz23/la-mordida/internal/platform/db"
	"github.com/hectorruiz23/la-mordida/internal/platform/httpserver"
	"github.com/hectorruiz23/la-mordida/migrations"
)

const usage = `La Mordida — contratación pública, sin ruido.

Usage:
  contratos import [flags]   importa publicaciones de una fuente
  contratos archive [flags]  archiva los documentos anunciados
  contratos serve [flags]    sirve las consultas sobre los datos guardados
  contratos version          imprime la versión

Opciones de import:
  -db string        fichero SQLite (por defecto contratos.db)
  -channel string   canal de PLACSP (por defecto sindicacion_643)
  -feed string      URL del feed de novedades del canal
  -archive string   paquete ZIP del histórico del canal
  -fixture string   fixture neutral local o URL (por defecto testdata/neutral/sample-award.json)
  -source string    clave de procedencia del cursor (por defecto el canal o fixture)
  -from string      inicio del periodo: AAAA-MM-DD (incluido) o instante RFC 3339
  -until string     fin del periodo: AAAA-MM-DD (excluido) o instante RFC 3339
  -since-last       reanuda desde el último punto íntegro del canal
  -overlap duration solape reprocesado al reanudar (por defecto 24h)
  -coverage         imprime los periodos cubiertos y no importa nada

Opciones de archive:
  -db string        fichero SQLite (por defecto contratos.db)
  -objects string   directorio de objetos (por defecto data/objects)
  -refresh          vuelve a descargar la evidencia ya guardada

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
	case "archive":
		return runArchive(args[1:])
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
	channelName := flags.String("channel", envOr("CONTRATOS_CHANNEL", string(placsp.Profiles)), "canal de PLACSP")
	feedURL := flags.String("feed", envOr("CONTRATOS_FEED", ""), "URL del feed de novedades")
	archivePath := flags.String("archive", envOr("CONTRATOS_ARCHIVE", ""), "paquete ZIP del histórico")
	source := flags.String("source", envOr("CONTRATOS_SOURCE", ""), "clave de procedencia del cursor")
	from := flags.String("from", "", "inicio del periodo (AAAA-MM-DD incluido o RFC 3339)")
	until := flags.String("until", "", "fin del periodo (AAAA-MM-DD excluido o RFC 3339)")
	sinceLast := flags.Bool("since-last", false, "reanuda desde el último punto íntegro del canal")
	overlap := flags.Duration("overlap", 24*time.Hour, "solape reprocesado al reanudar")
	coverage := flags.Bool("coverage", false, "imprime los periodos cubiertos y no importa nada")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("import: argumentos inesperados %v", flags.Args())
	}

	periodFrom, err := parseStart(*from)
	if err != nil {
		return fmt.Errorf("import: -from: %w", err)
	}
	periodUntil, untilExclusive, err := parseUntil(*until)
	if err != nil {
		return fmt.Errorf("import: -until: %w", err)
	}
	if !periodFrom.IsZero() && !periodUntil.IsZero() && !periodFrom.Before(periodUntil) {
		return errors.New("import: -from debe ser anterior a -until")
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := db.Migrate(ctx, database, migrations.FS); err != nil {
		return err
	}

	runs := ingestasqlite.New(database)
	reader, resolvedSource, err := importReader(*channelName, *feedURL, *archivePath, *fixturePath, *source)
	if err != nil {
		return err
	}

	if *coverage {
		return printCoverage(ctx, runs, resolvedSource)
	}

	kind := ingestaapp.Historical
	if *sinceLast {
		kind = ingestaapp.Incremental
	}

	store := contratossqlite.New(database)
	importer := ingestaapp.NewResumableImporter(reader, store, runs)
	report, importErr := importer.Import(ctx, ingestaapp.Request{
		Source:          resolvedSource,
		Kind:            kind,
		Reader:          reader,
		From:            periodFrom,
		Until:           periodUntil,
		UntilExclusive:  untilExclusive,
		SinceLast:       *sinceLast,
		Overlap:         *overlap,
		RecoverBoundary: true,
	})
	if importErr != nil {
		fmt.Fprintf(os.Stderr, "import: %s %s: %s tras %d publicaciones y %d páginas\n",
			report.Source, describeSpan(report.Span), importErr, report.Publications, report.Pages)
		return importErr
	}

	fmt.Printf("import: %s %s: %s, %d publicaciones, %d errores, %d páginas, punto %s en %s\n",
		report.Source, kind, describeSpan(report.Span), report.Publications, report.Errors,
		report.Pages, formatCursor(report.Cursor), *dbPath)
	return nil
}

func importReader(channelName, feedURL, archivePath, fixturePath, source string) (ingestaapp.Reader, string, error) {
	channel := placsp.Channel(channelName)
	switch {
	case feedURL != "":
		reader, err := placsp.NewFeed(channel, feedURL)
		if err != nil {
			return nil, "", err
		}
		if source == "" {
			source = channel.Source()
		}
		return reader, source, nil
	case archivePath != "":
		reader, err := placsp.NewArchive(channel, archivePath)
		if err != nil {
			return nil, "", err
		}
		if source == "" {
			source = channel.Source()
		}
		return reader, source, nil
	default:
		if source == "" {
			source = "fixture"
		}
		return fixture.New(fixturePath), source, nil
	}
}

func printCoverage(ctx context.Context, runs ingestaapp.RunLog, source string) error {
	spans, err := runs.Spans(ctx, source)
	if err != nil {
		return err
	}
	if len(spans) == 0 {
		fmt.Printf("cobertura %s: sin tramos registrados\n", source)
		return nil
	}
	fmt.Printf("cobertura %s:\n", source)
	for _, span := range spans {
		fmt.Printf("  %s %s: %s, %d publicaciones, %d errores, %d páginas, punto %s\n",
			formatPeriod(span.PeriodFrom, span.PeriodUntil, span.UntilExclusive),
			span.Kind, describeSpan(span.State), span.Publications, span.Errors, span.Pages,
			formatCursor(ingestaapp.Cursor{LastCompleteAt: span.LastCompleteAt, LastPage: span.LastPage}))
	}
	return nil
}

func describeSpan(state ingestaapp.SpanState) string {
	switch state {
	case ingestaapp.SpanComplete:
		return "completo"
	case ingestaapp.SpanPartial:
		return "parcial"
	case ingestaapp.SpanGap:
		return "hueco"
	default:
		return "desconocido"
	}
}

func formatPeriod(from, until time.Time, exclusive bool) string {
	start, end := "inicio abierto", "fin abierto"
	if !from.IsZero() {
		start = from.Format("2006-01-02")
	}
	switch {
	case until.IsZero():
	case exclusive:
		end = until.Format("2006-01-02") + " (sin incluir)"
	default:
		end = until.Format(time.RFC3339)
	}
	return start + ".." + end
}

func formatCursor(cursor ingestaapp.Cursor) string {
	if !cursor.LastCompleteAt.IsZero() {
		return cursor.LastCompleteAt.UTC().Format(time.RFC3339)
	}
	if cursor.LastPage != "" {
		return cursor.LastPage
	}
	return "ninguno"
}

func runArchive(args []string) error {
	flags := flag.NewFlagSet("archive", flag.ContinueOnError)
	dbPath := flags.String("db", envOr("CONTRATOS_DB", "contratos.db"), "fichero SQLite")
	objectsPath := flags.String("objects", envOr("CONTRATOS_OBJECTS", "data/objects"), "directorio de objetos")
	refresh := flags.Bool("refresh", false, "vuelve a descargar la evidencia ya guardada")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("archive: argumentos inesperados %v", flags.Args())
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := db.Migrate(ctx, database, migrations.FS); err != nil {
		return err
	}

	index := archivosqlite.New(database)
	links, err := index.Links(ctx, *refresh)
	if err != nil {
		return err
	}

	archiver := archivoapp.New(archivohttp.New(nil), archivodisk.New(*objectsPath), index)
	archived, pending := 0, 0
	for _, link := range links {
		archive := archiver.Archive
		if *refresh {
			archive = archiver.Refresh
		}
		if _, err := archive(ctx, link); err != nil {
			pending++
			fmt.Fprintf(os.Stderr, "archive: %s: %v\n", link.URL, err)
			continue
		}
		archived++
	}

	fmt.Printf("archive: %d documentos guardados, %d pendientes en %s\n", archived, pending, *objectsPath)
	if pending > 0 {
		return fmt.Errorf("archive: %d documentos quedaron pendientes, vuelve a ejecutarlo para reintentar", pending)
	}
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
	mux.HandleFunc("GET /api/ranking", handler.Ranking)
	mux.HandleFunc("GET /api/ranking/awards", handler.Awards)
	mux.HandleFunc("GET /api/rebajas", handler.Rebates)
	mux.HandleFunc("GET /adjudicatarios", handler.Adjudicatarios)
	mux.HandleFunc("GET /rebajas", handler.Rebajas)
	mux.HandleFunc("GET /adjudicaciones/{id}", handler.Adjudicacion)
	mux.Handle("GET /assets/", consultashttp.Assets())
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/adjudicatarios", http.StatusSeeOther)
	})

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

func parseStart(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if len(raw) == len("2006-01-02") {
		return time.Parse("2006-01-02", raw)
	}
	return parseInstant(raw)
}

func parseUntil(raw string) (time.Time, bool, error) {
	if raw == "" {
		return time.Time{}, false, nil
	}
	if len(raw) == len("2006-01-02") {
		parsed, err := time.Parse("2006-01-02", raw)
		return parsed, true, err
	}
	parsed, err := parseInstant(raw)
	return parsed, false, err
}

func parseInstant(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("instante %q: %w", raw, err)
	}
	return parsed, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
