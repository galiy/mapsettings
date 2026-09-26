// map-settings — отдельная программа чтения/инспекции/редактирования ячеек МАП.
// Copyright (C) 2026  Aleksandr Galinskii
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
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
		Addr:              fc.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("map-settings %s: http://%s/ (чтение: %s; запись: %s; конфиг %s)",
		version, fc.Listen, connSummary(fc.Read), connSummary(fc.Write), cfgPath)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("map-settings: %v", err)
	}
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
