// map-settings — отдельная программа чтения/инспекции/редактирования ячеек МАП.
// Copyright (C) 2026  Aleksandr Galinskii
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// version подставляется через -ldflags "-X main.version=..." (по умолчанию dev).
var version = "dev"

// debug включает отладочную нумерацию элементов интерфейса (№N). Подставляется
// через -ldflags "-X main.debug=true" (цель make build-debug).
var debug = ""

func debugEnabled() bool { return debug != "" }

// cfgPath — путь к локальному конфигу (mapsettings.json) рядом с модулем.
var cfgPath string

func main() {
	var (
		listen  = flag.String("listen", "", "адрес прослушивания HTTP (напр. :8099); переопределяет конфиг")
		user    = flag.String("user", "", "HTTP Basic: логин (пусто — без авторизации)")
		pass    = flag.String("pass", "", "HTTP Basic: пароль")
		showVer = flag.Bool("version", false, "показать версию и выйти")
		dbgIdx  = flag.Bool("dbgindex", false, "вывести индекс отладочных номеров (№ → элемент) и выйти")
		genDbg  = flag.Bool("gen-dbgorder", false, "дополнить фиксированный порядок отладочных номеров (dbgindex_order.go)")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("map-settings %s\n", version)
		return
	}
	if *genDbg {
		added, err := writeDbgOrder()
		if err != nil {
			log.Fatalf("map-settings: порядок номеров: %v", err)
		}
		fmt.Printf("dbgindex_order.go: добавлено %d id\n", added)
		return
	}
	if *dbgIdx {
		for _, m := range []string{mapModeTitanator, mapModeDominator} {
			entries, err := canonicalDbgEntries(m)
			if err != nil {
				log.Fatalf("map-settings: индекс: %v", err)
			}
			fmt.Printf("=== %s (%d элементов) ===\n", m, len(entries))
			for _, e := range entries {
				fmt.Printf("№%-4d %s\n", e.N, e.Label)
			}
		}
		return
	}

	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	cfgPath = configPath()
	// Лог рядом с программой: все значимые действия, результаты и ошибки.
	logPath := filepath.Join(filepath.Dir(cfgPath), "mapsettings.log")
	var logFile *os.File
	if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		logFile = lf
		log.SetOutput(io.MultiWriter(os.Stderr, lf))
		log.SetFlags(log.LstdFlags)
		log.Printf("map-settings: лог %s", logPath)
	} else {
		log.Printf("map-settings: не удалось открыть лог %s: %v", logPath, err)
	}
	fc := loadFileConfig(cfgPath)
	if fc == nil {
		c := defaultConfig()
		fc = &c
		if err := saveFileConfig(cfgPath, fc); err != nil {
			log.Printf("map-settings: не удалось создать конфиг %s: %v", cfgPath, err)
		} else {
			log.Printf("map-settings: создан стартовый конфиг %s", cfgPath)
		}
	}
	normalizeConfig(fc)
	if set["listen"] && *listen != "" {
		fc.Listen = *listen
	}
	if fc.User == "" && *user != "" {
		fc.User = *user
	}
	if fc.Pass == "" && *pass != "" {
		fc.Pass = *pass
	}
	setCfg(*fc)

	mux := newMux(fc.User, fc.Pass)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Медленные операции с МАП идут до readTimeout; даём запас на запись
		// ответа, чтобы клиент не получил обрыв на длинной операции.
		WriteTimeout: readTimeout + 30*time.Second,
		IdleTimeout:  60 * time.Second,
	}
	// Завершение по SIGINT/SIGTERM: корректно закрываем сервер и файл лога.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Свободный порт: пробуем заданный, при занятости берём любой свободный.
	ln, url, err := listenUI(fc.Listen)
	if err != nil {
		log.Fatalf("map-settings: %v", err)
	}
	log.Printf("map-settings %s: UI доступен по адресу %s (чтение: %s; запись: %s; конфиг %s)",
		version, url, connSummary(fc.Read), connSummary(fc.Write), cfgPath)
	// Сервер стартуем сразу; браузер открываем после готовности прослушивания.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	waitReady(ln.Addr())
	// Открываем страницу только при наличии графического дисплея в сессии.
	if displayAvailable() {
		go openBrowser(url)
	} else {
		log.Printf("map-settings: графический дисплей не обнаружен — откройте %s вручную", url)
	}
	select {
	case err := <-errCh:
		log.Printf("map-settings: сервер остановлен с ошибкой: %v", err)
	case <-ctx.Done():
		log.Printf("map-settings: получен сигнал завершения — останавливаю сервер")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("map-settings: корректное завершение сервера: %v", err)
	}
	if logFile != nil {
		_ = logFile.Close()
	}
}

// waitReady ждёт, пока сервер начнёт принимать соединения (до ~3 с).
func waitReady(addr net.Addr) {
	for i := 0; i < 30; i++ {
		c, err := net.DialTimeout("tcp", addr.String(), 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// listenUI пытается занять заданный адрес; если порт занят — берёт свободный.
// Возвращает слушатель и URL для доступа (с фактическим портом).
func listenUI(addr string) (net.Listener, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", "8099"
	}
	if ln, err := net.Listen("tcp", net.JoinHostPort(host, port)); err == nil {
		return ln, urlFor(host, ln.Addr()), nil
	}
	// Запрошенный порт занят — берём любой свободный на том же хосте.
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, "", err
	}
	return ln, urlFor(host, ln.Addr()), nil
}

// urlFor формирует URL по адресу прослушивания (0.0.0.0/:: → 127.0.0.1).
func urlFor(host string, la net.Addr) string {
	if ta, ok := la.(*net.TCPAddr); ok {
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		return fmt.Sprintf("http://%s/", net.JoinHostPort(host, strconv.Itoa(ta.Port)))
	}
	return "http://" + la.String() + "/"
}

// displayAvailable — есть ли графический дисплей в окружении сессии.
func displayAvailable() bool {
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	default:
		return true
	}
}

// openBrowser открывает URL в браузере по умолчанию, перебирая варианты.
func openBrowser(url string) {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"open", url}}
	case "windows":
		candidates = [][]string{{"rundll32", "url.dll,FileProtocolHandler", url}}
	default:
		candidates = [][]string{
			{"xdg-open", url}, {"gio", "open", url},
			{"firefox", url}, {"google-chrome", url}, {"chromium", url},
		}
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		out, err := exec.Command(c[0], c[1:]...).CombinedOutput()
		if err == nil {
			log.Printf("map-settings: UI открыт (%s)", c[0])
			return
		}
		log.Printf("map-settings: %s не сработал: %v (%s)", c[0], err, strings.TrimSpace(string(out)))
	}
	log.Printf("map-settings: не удалось открыть браузер автоматически — откройте %s", url)
}

// writeJSON/writeJSONStatus — небольшие помощники ответов API.
func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSON(w http.ResponseWriter, v any) { writeJSONStatus(w, http.StatusOK, v) }

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
