// map-settings — HTTP-слой отдельной программы.
// Copyright (C) 2026  Aleksandr Galinskii
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

// appCfg — текущие настройки (два блока связи). Доступ через mutex.
var (
	appCfg fileConfig
	appMu  sync.RWMutex
)

func cfgSnapshot() fileConfig {
	appMu.RLock()
	defer appMu.RUnlock()
	return appCfg
}

func setCfg(c fileConfig) {
	appMu.Lock()
	appCfg = c
	appMu.Unlock()
}

// readTimeout — верхняя граница на медленные операции с МАП.
const readTimeout = 90 * time.Second

func newMux(user, pass string) http.Handler {
	mux := http.NewServeMux()
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("map-settings: web: %v", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	auth := basicAuth(user, pass)
	mux.Handle("/api/config", auth(http.HandlerFunc(apiConfig)))
	mux.Handle("/api/ports", auth(http.HandlerFunc(apiPorts)))
	mux.Handle("/api/settings", auth(http.HandlerFunc(apiSettings)))
	mux.Handle("/api/param", auth(http.HandlerFunc(apiParam)))
	mux.Handle("/api/dbgindex", auth(http.HandlerFunc(apiDbgIndex)))
	mux.Handle("/api/apply", auth(http.HandlerFunc(apiApply)))
	mux.Handle("/api/action", auth(http.HandlerFunc(apiAction)))
	mux.Handle("/api/relays", auth(http.HandlerFunc(apiRelays)))
	mux.Handle("/api/time", auth(http.HandlerFunc(apiTime)))
	mux.HandleFunc("/", indexHandler)
	return mux
}

func basicAuth(user, pass string) func(http.Handler) http.Handler {
	if user == "" && pass == "" {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			valid := ok &&
				subtle.ConstantTimeCompare([]byte(u), []byte(user)) == 1 &&
				subtle.ConstantTimeCompare([]byte(p), []byte(pass)) == 1
			if !valid {
				w.Header().Set("WWW-Authenticate", `Basic realm="map-settings"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// apiConfig — GET отдаёт конфиг, POST сохраняет (полностью) и применяет.
func apiConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req fileConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		normalizeConfig(&req)
		if err := validateConnConfig(req.Read, false); err != nil {
			http.Error(w, "чтение: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateConnConfig(req.Write, true); err != nil {
			http.Error(w, "запись: "+err.Error(), http.StatusBadRequest)
			return
		}
		cur := cfgSnapshot()
		req.Listen = cur.Listen
		req.User, req.Pass = cur.User, cur.Pass
		if err := saveFileConfig(cfgPath, &req); err != nil {
			log.Printf("map-settings: сохранение конфига %s: %v", cfgPath, err)
		}
		setCfg(req)
		log.Printf("настройки сохранены: чтение[%s] запись[%s]", connParams(req.Read), connParams(req.Write))
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	c := cfgSnapshot()
	writeJSON(w, map[string]any{
		"listen": c.Listen,
		"read":   c.Read,
		"write":  c.Write,
		"debug":  debugEnabled(),
	})
}

// apiPorts — список доступных последовательных портов.
func apiPorts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ports": listSerialPorts()})
}

// resolveMode возвращает режим (тип МАП): из запроса или определяет по _DevOpt.
func resolveMode(ctx context.Context, conn cellConn, mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode != "" {
		return normalizeMapMode(mode), nil
	}
	return detectMapMode(ctx, conn)
}

func apiSettings(w http.ResponseWriter, r *http.Request) {
	c := cfgSnapshot()
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	reqMode := strings.TrimSpace(r.URL.Query().Get("mode"))
	log.Printf("чтение снимка: %s; запрошенный режим=%q", connParams(c.Read), reqMode)
	conn, err := openConn(c.Read)
	if err != nil {
		log.Printf("чтение снимка: ошибка подключения: %v", err)
		http.Error(w, "чтение: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer conn.Close()
	mode := reqMode
	if mode == "" {
		mode, err = detectMapMode(ctx, conn)
		if err != nil {
			log.Printf("чтение снимка: определение типа: %v", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	} else {
		mode = normalizeMapMode(mode)
	}
	snap, err := readMapSettingsSnapshot(ctx, conn, mode)
	if err != nil {
		log.Printf("чтение снимка (режим %s): %v", mode, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("чтение снимка: режим %s, групп %d, ошибок %d%s",
		snap.Mode, len(snap.Settings)+len(snap.Monitor), len(snap.Errors), errsSuffix(snap.Errors))
	writeJSON(w, snap)
}

// apiDbgIndex — детерминированный индекс отладочных номеров (только debug).
func apiDbgIndex(w http.ResponseWriter, r *http.Request) {
	if !debugEnabled() {
		http.NotFound(w, r)
		return
	}
	mode := normalizeMapMode(r.URL.Query().Get("mode"))
	entries, err := canonicalDbgEntries(mode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, entries)
}

// apiParam — чтение одного параметра: GET /api/param?mode=&key=
func apiParam(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "не задан key", http.StatusBadRequest)
		return
	}
	c := cfgSnapshot()
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	conn, err := openConn(c.Read)
	if err != nil {
		log.Printf("чтение параметра: %s; key=%q: ошибка подключения: %v", connParams(c.Read), key, err)
		http.Error(w, "чтение: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer conn.Close()
	mode, err := resolveMode(ctx, conn, r.URL.Query().Get("mode"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	v, err := readMapSettingView(ctx, conn, mode, key)
	if err != nil {
		log.Printf("чтение параметра: %s; key=%q режим=%s: %v", connParams(c.Read), key, mode, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("чтение параметра: %s; key=%q режим=%s → %s", connParams(c.Read), key, mode, fmtVal(v))
	writeJSON(w, v)
}

func apiApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode        string             `json:"mode"`
		Changes     map[string]float64 `json:"changes"`
		AllowDanger bool               `json:"allowDanger"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Changes) == 0 {
		writeJSON(w, map[string]any{"results": map[string]string{}})
		return
	}
	c := cfgSnapshot()
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	conn, err := openConn(c.Write)
	if err != nil {
		log.Printf("запись: %s; ошибка подключения: %v", connParams(c.Write), err)
		http.Error(w, "запись: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer conn.Close()
	mode, err := resolveMode(ctx, conn, req.Mode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("запись: %s; режим=%s allowDanger=%v изменения[%s]", connParams(c.Write), mode, req.AllowDanger, keysText(req.Changes))
	results, err := applyMapSettings(ctx, conn, mode, req.Changes, req.AllowDanger)
	resp := map[string]any{"results": results}
	if err != nil {
		resp["error"] = err.Error()
		log.Printf("запись: ошибка: %v", err)
		writeJSONStatus(w, http.StatusBadGateway, resp)
		return
	}
	if len(results) > 0 {
		log.Printf("запись: не применено: %s", resultsText(results))
	} else {
		log.Printf("запись: успешно: %s", keysText(req.Changes))
	}
	writeJSON(w, resp)
}

func apiAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
		Key  string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	c := cfgSnapshot()
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	conn, err := openConn(c.Write)
	if err != nil {
		log.Printf("команда: %s; key=%q: ошибка подключения: %v", connParams(c.Write), req.Key, err)
		http.Error(w, "запись: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer conn.Close()
	log.Printf("команда: %s; key=%q режим=%s", connParams(c.Write), req.Key, req.Mode)
	if err := runMapSettingsAction(ctx, conn, req.Mode, req.Key); err != nil {
		log.Printf("команда %q: ошибка: %v", req.Key, err)
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	log.Printf("команда %q: выполнена", req.Key)
	writeJSON(w, map[string]any{"ok": true})
}

// apiRelays — состояние доп. реле (для кнопок вкл/выкл).
func apiRelays(w http.ResponseWriter, r *http.Request) {
	c := cfgSnapshot()
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	conn, err := openConn(c.Read)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	defer conn.Close()
	mode, err := resolveMode(ctx, conn, r.URL.Query().Get("mode"))
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	states, err := readRelayStates(ctx, conn, mode)
	if err != nil {
		// Состояние доступно только при связи с Малиной — отдаём пусто.
		writeJSON(w, map[string]any{"mode": mode, "relays": []any{}, "note": err.Error()})
		return
	}
	relays := make([]map[string]any, 0, len(states))
	for i, s := range states {
		relays = append(relays, map[string]any{"num": i + 1, "on": s != 0})
	}
	writeJSON(w, map[string]any{"mode": mode, "relays": relays})
}

func apiTime(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		c := cfgSnapshot()
		ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
		defer cancel()
		conn, err := openConn(c.Read)
		if err != nil {
			http.Error(w, "чтение: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer conn.Close()
		log.Printf("чтение времени: %s", connParams(c.Read))
		st, err := readMapTime(ctx, conn)
		if err != nil {
			log.Printf("чтение времени: ошибка: %v", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		log.Printf("чтение времени: %02d:%02d", st.Hour, st.Minute)
		writeJSON(w, st)
		return
	}
	var req struct {
		Mode   string `json:"mode"`
		Hour   int    `json:"hour"`
		Minute int    `json:"minute"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	c := cfgSnapshot()
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	conn, err := openConn(c.Write)
	if err != nil {
		http.Error(w, "запись: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer conn.Close()
	log.Printf("запись времени: %s; режим=%s значение=%02d:%02d", connParams(c.Write), req.Mode, req.Hour, req.Minute)
	if err := writeMapTime(ctx, conn, req.Hour, req.Minute); err != nil {
		log.Printf("запись времени: ошибка: %v", err)
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	log.Printf("запись времени: успешно %02d:%02d", req.Hour, req.Minute)
	writeJSON(w, map[string]any{"ok": true})
}

// errsSuffix — « (…; …)» для списка ошибок чтения (или пусто).
func errsSuffix(errs []string) string {
	if len(errs) == 0 {
		return ""
	}
	return " (" + strings.Join(errs, "; ") + ")"
}

// fmtVal — краткое значение параметра для лога.
func fmtVal(v *mapSettingView) string {
	if v == nil {
		return "—"
	}
	if v.Error != "" {
		return "ошибка: " + v.Error
	}
	if v.Value != nil {
		return fmt.Sprintf("%g", *v.Value)
	}
	return "—"
}

// resultsText — «key: причина; …» для результатов с ошибками.
func resultsText(results map[string]string) string {
	keys := make([]string, 0, len(results))
	for k := range results {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+results[k])
	}
	return strings.Join(parts, "; ")
}

// keysText — отсортированный список ключей изменения.
func keysText(changes map[string]float64) string {
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, fmt.Sprintf("%s=%g", k, changes[k]))
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
