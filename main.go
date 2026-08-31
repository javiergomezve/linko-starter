package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	if logFile == "" {
		return slog.New(stderrHandler), noop, nil
	}

	file, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open log file: %w", err)
	}

	bufferedFile := bufio.NewWriterSize(file, 8192)
	fileHandler := slog.NewTextHandler(bufferedFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	closeLogger := func() error {
		if err := bufferedFile.Flush(); err != nil {
			file.Close()
			return fmt.Errorf("failed to flush log buffer: %w", err)
		}
		return file.Close()
	}

	logger := slog.New(slog.NewMultiHandler(stderrHandler, fileHandler))

	return logger, closeLogger, nil
}
