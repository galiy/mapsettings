// map-settings — локальный конфиг программы.
// Copyright (C) 2026  Aleksandr Galinskii
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// configFileName — файл локальных настроек рядом с бинарником (в .gitignore).
const configFileName = "mapsettings.json"

// connConfig — параметры одного способа связи. Используются поля, относящиеся
// к выбранным Protocol/Transport:
//
//	protocol: modbus | microart | malina
//	transport (для modbus/microart): tcp | com
//	tcp: ip, port, unit; com: serial, baud, unit; malina: host, login, password.
type connConfig struct {
	Protocol  string `json:"protocol,omitempty"`
	Transport string `json:"transport,omitempty"`
	IP        string `json:"ip,omitempty"`
	Port      int    `json:"port,omitempty"`
	Serial    string `json:"serial,omitempty"`
	Baud      int    `json:"baud,omitempty"`
	Unit      int    `json:"unit,omitempty"`
	Host      string `json:"host,omitempty"`
	Login     string `json:"login,omitempty"`
	Password  string `json:"password,omitempty"`
}

// fileConfig — сохраняемые настройки: два блока связи (чтение и запись) и
// параметры запуска. Тип МАП не хранится — определяется при чтении.
type fileConfig struct {
	Listen string     `json:"listen,omitempty"`
	Read   connConfig `json:"read"`
	Write  connConfig `json:"write"`
	User   string     `json:"user,omitempty"`
	Pass   string     `json:"pass,omitempty"`

	// Устаревшие поля (миграция со старой версии).
	Map  string `json:"map,omitempty"`
	Unit int    `json:"unit,omitempty"`
	IP   string `json:"ip,omitempty"`
	Port int    `json:"port,omitempty"`
}

// defaultConfig — значения по умолчанию.
func defaultConfig() fileConfig {
	return fileConfig{
		Listen: ":8099",
		Read: connConfig{
			Protocol: "modbus", Transport: "tcp",
			IP: "192.168.13.60", Port: 502, Unit: 1,
		},
		Write: connConfig{
			Protocol: "malina",
			Host:     "192.168.13.60", Login: "admin",
		},
	}
}

// normalizeConfig мигрирует устаревшие поля и заполняет умолчания.
func normalizeConfig(c *fileConfig) {
	d := defaultConfig()
	if c.Listen == "" {
		c.Listen = d.Listen
	}
	// Миграция: старые ip/port/map → блок чтения (modbus/tcp).
	if c.Read.Protocol == "" {
		ip, port := c.IP, c.Port
		if ip == "" && c.Map != "" {
			if h, p, err := splitHostPort(c.Map); err == nil {
				ip, port = h, p
			}
		}
		if ip == "" {
			ip = d.Read.IP
		}
		if port == 0 {
			port = d.Read.Port
		}
		unit := c.Unit
		if unit == 0 {
			unit = d.Read.Unit
		}
		c.Read = connConfig{Protocol: "modbus", Transport: "tcp", IP: ip, Port: port, Unit: unit}
	}
	if c.Write.Protocol == "" {
		c.Write = d.Write
	}
	if c.Read.Unit == 0 {
		c.Read.Unit = 1
	}
	if c.Write.Unit == 0 {
		c.Write.Unit = 1
	}
}

// configPath — mapsettings.json рядом с исполняемым файлом; при `go run` —
// в текущем каталоге.
func configPath() string {
	exe, err := os.Executable()
	if err != nil {
		return configFileName
	}
	dir := filepath.Dir(exe)
	// При `go run` бинарник лежит во временном каталоге: конфиг берём из
	// текущего каталога, иначе старый файл рядом с временным бинарником мог бы
	// перекрыть рабочий.
	if isTempDir(dir) {
		if wd, err := os.Getwd(); err == nil {
			return filepath.Join(wd, configFileName)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, configFileName)); err == nil {
		return filepath.Join(dir, configFileName)
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, configFileName)
	}
	return filepath.Join(dir, configFileName)
}

// isTempDir сообщает, что каталог находится внутри системного временного
// каталога (так собирает бинарник `go run`).
func isTempDir(dir string) bool {
	tmp := os.TempDir()
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = resolved
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	rel, err := filepath.Rel(tmp, dir)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// loadFileConfig читает конфиг; отсутствие файла — не ошибка (nil).
func loadFileConfig(path string) *fileConfig {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c fileConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return nil
	}
	normalizeConfig(&c)
	return &c
}

// saveFileConfig сохраняет конфиг (best-effort, 0644).
func saveFileConfig(path string, c *fileConfig) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

// splitHostPort разбирает "host:port"; при отсутствии порта берёт 502.
func splitHostPort(s string) (string, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, fmt.Errorf("пустой адрес")
	}
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		if strings.Contains(s, ":") {
			return "", 0, err
		}
		host, portStr = s, "502"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("некорректный порт %q", portStr)
	}
	return host, port, nil
}

// maskSecret скрывает секрет в логе.
func maskSecret(s string) string {
	if s == "" {
		return "(пусто)"
	}
	return "***"
}

// connParams — все параметры способа связи для лога (пароль маскируется).
func connParams(c connConfig) string {
	parts := []string{"protocol=" + c.Protocol}
	switch c.Protocol {
	case "malina":
		parts = append(parts, "host="+c.Host, "login="+c.Login, "password="+maskSecret(c.Password))
	case "modbus", "microart":
		t := c.Transport
		if t == "" {
			t = "tcp"
		}
		parts = append(parts, "transport="+t)
		if t == "com" {
			parts = append(parts, "serial="+c.Serial, fmt.Sprintf("baud=%d", c.Baud))
		} else {
			parts = append(parts, "ip="+c.IP, fmt.Sprintf("port=%d", c.Port))
		}
		parts = append(parts, fmt.Sprintf("unit=%d", c.Unit))
	default:
		parts = append(parts, fmt.Sprintf("host=%s", c.Host), fmt.Sprintf("ip=%s", c.IP))
	}
	return strings.Join(parts, " ")
}

// connSummary — короткое описание способа связи для логов.
func connSummary(c connConfig) string {
	switch c.Protocol {
	case "malina":
		return "malina " + c.Host
	case "microart", "modbus":
		if c.Transport == "com" {
			return c.Protocol + " com " + c.Serial
		}
		return c.Protocol + " tcp " + c.IP
	}
	return strings.TrimSpace(c.Protocol)
}
