// map-settings — индекс отладочных номеров (№) интерфейса.
// Copyright (C) 2026  Aleksandr Galinskii
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// dbgEntry — одна строка индекса отладочной нумерации.
type dbgEntry struct {
	N     int    `json:"n"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

// dbgTableHeaders совпадает с заголовками колонок в web/app.js.
var dbgTableHeaders = []string{"Параметр", "Ячейка", "Значение", "Ед.", "Диапазон", "Действия"}

// dbgGroup — логический блок: rw-настройки и ro-мониторинг.
type dbgGroup struct {
	name     string
	settings []mapParamSpec
	monitor  []mapParamSpec
}

// canonicalGroups собирает блоки для режима в порядке появления в каталоге.
func canonicalGroups(mode string) ([]*dbgGroup, error) {
	cat, err := loadMapSettingsCatalog()
	if err != nil {
		return nil, err
	}
	order := []string{}
	byName := map[string]*dbgGroup{}
	for _, p := range cat.Params {
		if !paramInMode(p, mode) {
			continue
		}
		g := byName[p.Group]
		if g == nil {
			g = &dbgGroup{name: p.Group}
			byName[p.Group] = g
			order = append(order, p.Group)
		}
		if p.Access == "rw" {
			g.settings = append(g.settings, p)
		} else {
			g.monitor = append(g.monitor, p)
		}
	}
	out := make([]*dbgGroup, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out, nil
}

// jsEncodeURIComponent повторяет encodeURIComponent из JS (для id кнопок
// разделов).
func jsEncodeURIComponent(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func jsNum(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// structuralDbgEntries строит список отладочных id для режима в порядке
// отрисовки (без учёта глобального порядка нумерации).
func structuralDbgEntries(mode string) ([]dbgEntry, error) {
	groups, err := canonicalGroups(mode)
	if err != nil {
		return nil, err
	}
	var out []dbgEntry
	seen := map[string]bool{}
	add := func(id, label string) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, dbgEntry{N: len(out) + 1, ID: id, Label: label})
	}
	addParam := func(p mapParamSpec, group, mode string) {
		danger := dangerKeys[p.Key]
		q := group + " → " + p.Name
		add(p.Key+":name", "текст (имя параметра): "+q)
		if danger {
			add(p.Key+":danger", "метка «▲ Опасно!» (span): "+q)
		}
		if paramHelp(p, mode) != "" {
			add(p.Key+":help", "кнопка «?» (подсказка): "+q)
		}
		switch {
		case len(p.Fields) > 0:
			add(p.Key+":value", "несколько полей (div): "+q)
			for i, f := range p.Fields {
				add(fmt.Sprintf("%s:field:%d", p.Key, i), "поле «"+f.Label+"»: "+q)
				if len(f.Enum) > 0 {
					keys := make([]int, 0, len(f.Enum))
					for k := range f.Enum {
						if n, err := strconv.Atoi(k); err == nil {
							keys = append(keys, n)
						}
					}
					sort.Ints(keys)
					for _, n := range keys {
						k := strconv.Itoa(n)
						add(fmt.Sprintf("%s:field:%d:opt:%s", p.Key, i, k),
							"вариант «"+f.Label+"»: "+f.Enum[k]+": "+q)
					}
				}
			}
		case len(paramEnum(p, mode)) > 0:
			add(p.Key+":value", "радиогруппа (div): "+q)
			for _, o := range paramOptions(p, mode) {
				add(p.Key+":opt:"+jsNum(o.Value),
					"радиокнопка «"+o.Label+"» (input radio, код "+jsNum(o.Value)+"): "+q)
			}
		case len(p.Bits) > 0:
			add(p.Key+":value", "группа чекбоксов (div): "+q)
			bits := append([]mapBit(nil), p.Bits...)
			sort.Slice(bits, func(i, j int) bool { return bits[i].Bit < bits[j].Bit })
			for _, bit := range bits {
				add(p.Key+":bit:"+strconv.Itoa(bit.Bit),
					"чекбокс «"+bit.Name+"» (input checkbox, бит "+strconv.Itoa(bit.Bit)+"): "+q)
			}
		case p.Access == "rw":
			add(p.Key+":value", "поле ввода числа (input): "+q)
		default:
			add(p.Key+":value", "текст значения (span, только чтение): "+q)
		}
		add(p.Key+":read", "кнопка «Читать»: "+q)
		add(p.Key+":save", "кнопка «Сохр.»: "+q)
		if danger && p.Access == "rw" {
			add(p.Key+":lock", "кнопка 🔒/🔓 (разблокировать ред.): "+q)
		}
	}

	// Главная: блоки настроек связи, форма времени (и подсказки «?»).
	add("hdr:title", "h1 заголовок «Настройки МАП»")
	add("hdr:sub", "текст (подзаголовок) «Чтение и запись…»")
	add("cfg:read:title", "h2 заголовок блока «Чтение»")
	add("cfg:read:proto", "выпадающий список (select) «Протокол» (чтение)")
	add("rdProto:help", "кнопка «?» у «Протокол» (чтение)")
	add("cfg:write:title", "h2 заголовок блока «Запись»")
	add("cfg:write:proto", "выпадающий список (select) «Протокол» (запись)")
	add("wrProto:help", "кнопка «?» у «Протокол» (запись)")
	add("model", "текст (span) «Тип МАП»")
	add("btn:read", "кнопка «Читать» (главная)")
	add("msRead:help", "кнопка «?» у «Читать» (главная)")
	add("btn:clear", "кнопка «Очистить» (главная)")
	add("msClear:help", "кнопка «?» у «Очистить» (главная)")
	add("sec:time:title", "h2 заголовок «Текущее время МАП»")
	add("field:timeH", "поле ввода числа (input) «Часы»")
	add("msTimeH:help", "кнопка «?» у «Часы»")
	add("field:timeM", "поле ввода числа (input) «Минуты»")
	add("msTimeM:help", "кнопка «?» у «Минуты»")
	add("btn:timeRead", "кнопка «Прочитать время»")
	add("msTimeRead:help", "кнопка «?» у «Прочитать время»")
	add("btn:timeWrite", "кнопка «Записать время»")
	add("msTimeWrite:help", "кнопка «?» у «Записать время»")

	// Список разделов (на главной и в каждом разделе).
	for _, g := range groups {
		add("nav:group:"+jsEncodeURIComponent(g.name), "кнопка навигации (button) — раздел «"+g.name+"»")
	}
	add("nav:actions", "кнопка навигации (button) — раздел «Управляющие воздействия»")

	// Шапка раздела.
	add("sec:read", "кнопка «Читать» (раздел)")
	add("msSecRead:help", "кнопка «?» у «Читать» (раздел)")
	add("sec:home", "кнопка «На главную» (раздел)")
	add("msSecHome:help", "кнопка «?» у «На главную» (раздел)")

	// Содержимое разделов.
	for _, g := range groups {
		if len(g.settings) > 0 {
			add(fmt.Sprintf("settings:%s:Настройки:title", g.name), "h3 заголовок блока (настройки): "+g.name)
			for _, h := range dbgTableHeaders {
				add(fmt.Sprintf("settings:%s:Настройки:th:%s", g.name, h), "th заголовок колонки «"+h+"»: "+g.name)
			}
			for _, p := range g.settings {
				addParam(p, g.name, mode)
			}
		}
		if len(g.monitor) > 0 {
			add(fmt.Sprintf("monitor:%s:Мониторинг:title", g.name), "h3 заголовок блока (мониторинг): "+g.name)
			for _, h := range dbgTableHeaders {
				add(fmt.Sprintf("monitor:%s:Мониторинг:th:%s", g.name, h), "th заголовок колонки «"+h+"»: "+g.name)
			}
			for _, p := range g.monitor {
				addParam(p, g.name, mode)
			}
		}
	}

	// Экран «Управляющие воздействия».
	for _, a := range mapSettingsActions {
		add("action:"+a.Key+":btn", "кнопка команды (button) «"+a.Name+"» — Управляющие воздействия")
		add("action:"+a.Key+":help", "кнопка «?» у команды «"+a.Name+"» — Управляющие воздействия")
	}
	return out, nil
}

// canonicalDbgEntries выдаёт номера по фиксированному порядку dbgIDOrder
// (append-only): новые id получают номера после всех известных и не сдвигают
// уже выданные. Сам порядок хранится в dbgindex_order.go (генерируется
// флагом -gen-dbgorder).
func canonicalDbgEntries(mode string) ([]dbgEntry, error) {
	structEntries, err := structuralDbgEntries(mode)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	order := []string{}
	for _, e := range structEntries {
		if _, ok := labels[e.ID]; !ok {
			labels[e.ID] = e.Label
			order = append(order, e.ID)
		}
	}
	pos := make(map[string]int, len(dbgIDOrder))
	for i, id := range dbgIDOrder {
		pos[id] = i + 1
	}
	out := make([]dbgEntry, 0, len(order))
	seen := map[string]bool{}
	for _, id := range dbgIDOrder {
		if label, ok := labels[id]; ok {
			out = append(out, dbgEntry{N: pos[id], ID: id, Label: label})
			seen[id] = true
		}
	}
	n := len(dbgIDOrder)
	for _, id := range order {
		if seen[id] {
			continue
		}
		n++
		out = append(out, dbgEntry{N: n, ID: id, Label: labels[id]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out, nil
}
