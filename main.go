package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	pkgerr "github.com/pkg/errors"

	"github.com/lmittmann/tint"
	isatty "github.com/mattn/go-isatty"
	"gopkg.in/natefinch/lumberjack.v2"

	"boot.dev/linko/internal/build"
	"boot.dev/linko/internal/linkoerr"
	"boot.dev/linko/internal/store"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	httpPort := flag.Int("port", 8899, "port to listen on")
	dataDir := flag.String("data", "./data", "directory to store data")
	flag.Parse()

	status := run(ctx, cancel, *httpPort, *dataDir)
	cancel()

	os.Exit(status)
}

func run(ctx context.Context, cancel context.CancelFunc, httpPort int, dataDir string) int {
	logger, closeLogger, err := initializeLogger(os.Getenv("LINKO_LOG_FILE"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		return 1
	}

	st, err := store.New(dataDir, logger)
	if err != nil {
		logger.Error("failed to create store",
			"error", err,
		)
		return 1
	}
	s := newServer(*st, httpPort, cancel, logger)
	var serverErr error
	go func() {
		serverErr = s.start()
	}()

	logger.Debug(fmt.Sprintf("Linko is running on http://localhost:%d", httpPort))
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shutdown server",
			"error", err,
		)
		return 1
	}
	if serverErr != nil {
		logger.Error("server error",
			"error", serverErr,
		)
		return 1
	}

	logger.Debug("Linko is shutting down")

	closeLogger()

	return 0
}

func initializeLogger(logFile string) (*slog.Logger, func() error, error) {
	noop := func() error { return nil }

	noColor := true
	fd := os.Stderr.Fd()
	if isatty.IsCygwinTerminal(fd) || isatty.IsTerminal(fd) {
		noColor = false
	}

	stderrHandler := tint.NewTextHandler(os.Stderr, &tint.Options{
		NoColor:     noColor,
		Level:       slog.LevelDebug,
		ReplaceAttr: replaceAttr,
	})

	if logFile == "" {
		return slog.New(stderrHandler), noop, nil
	}

	lumberjackLogger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    1, // megabytes
		MaxBackups: 3,
		MaxAge:     28, // days
		Compress:   true,
	}

	fileHandler := slog.NewJSONHandler(lumberjackLogger, &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: replaceAttr,
	})

	closeLogger := func() error {
		return lumberjackLogger.Close()
	}

	logger := slog.New(slog.NewMultiHandler(stderrHandler, fileHandler))

	hostname, _ := os.Hostname()
	logger = logger.With(
		slog.String("git_sha", build.GitSHA),
		slog.String("build_time", build.BuildTime),
		slog.String("env", os.Getenv("ENV")),
		slog.String("hostname", hostname),
	)

	return logger, closeLogger, nil
}

type stackTracer interface {
	error
	StackTrace() pkgerr.StackTrace
}

type multiError interface {
	error
	Unwrap() []error
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Key == "error" {
		err, ok := a.Value.Any().(error)
		if !ok {
			return a
		}

		multiErr, ok := errors.AsType[multiError](err)
		if ok {
			var attrs []slog.Attr

			for index, item := range multiErr.Unwrap() {
				attrs = append(
					attrs,
					slog.GroupAttrs(
						fmt.Sprintf("error_%d", index+1),
						errorAttrs(item)...,
					),
				)
			}

			return slog.GroupAttrs("errors", attrs...)
		}

		return slog.GroupAttrs("error", errorAttrs(err)...)
	}

	if slices.Contains([]string{"password", "key", "apikey", "secret", "pin", "creditcardno", "user"}, a.Key) {
		return slog.String(a.Key, "[REDACTED]")
	}

	if strings.Contains(strings.ToLower(a.Key), "url") {
		parsedUrl, err := url.Parse(a.Value.String())
		if err != nil || parsedUrl.User == nil {
			return a
		}

		parsedUrl.User = url.UserPassword(parsedUrl.User.Username(), "REDACTED")

		return slog.String(a.Key, parsedUrl.String())
	}

	return a
}

func errorAttrs(err error) []slog.Attr {
	attrs := []slog.Attr{
		{
			Key:   "message",
			Value: slog.StringValue(err.Error()),
		},
	}

	attrs = append(attrs, linkoerr.Attrs(err)...)

	if stackErr, ok := errors.AsType[stackTracer](err); ok {
		attrs = append(attrs, slog.Attr{
			Key:   "stack_trace",
			Value: slog.StringValue(fmt.Sprintf("%+v", stackErr.StackTrace())),
		})
	}

	return attrs
}
