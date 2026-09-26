// mapsettings
// Copyright (C) 2026  Aleksandr Galinskii
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

// catalog.go — ядро «Настройки МАП»: чтение, инспектирование и
// редактирование ячеек МАП через веб-UI дашборда.
//
// Источник каталога ячеек — mapsettings/catalog.json, сгенерированный из
// protocol_MAP_cells_2026_07_15.doc (последняя версия документации; Malina web
// API как эталон НЕ используется). Каталог встроен в бинарник через go:embed.
//
// Запись выполняется по протоколу МАП в Modbus-обёртке со служебным обрамлением,
// как это делает mapd: ComMAP_EEPromWR (0x03) → запись ячейки(ек) →
// ComMAP_Call_load_EEProm (0x07), затем чтение-обратно (верификация).

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed catalog.json
var mapSettingsCatalogJSON []byte

// Режимы МАП (от них зависит набор отображаемых параметров).
const (
	mapModeTitanator = "titanator"
	mapModeDominator = "dominator"
)

// Ячейки времени (protocol_MAP_cells_2026_07_15.doc, стр.1394–1396).
const (
	mapCellTimeCyr   = 0x1B6 // _LCD_TimeCyr = (hh<<3)|(mm/10) — EEPROM
	mapCellTimeMin   = 0x44B // _TimeCyr_MINUT = mm%10 — RAM
	mapCellPutEEPROM = 0x403 // _put_eeprom — флаг записи в EEPROM
	mapCellDevOpt    = 0x007 // _DevOpt: 3 — Титанатор, иначе Доминатор
	mapCellUACC      = 0x006 // _UACC: код напряжения АКБ (число блоков, сдвиг)
)

// detectMapMode читает _DevOpt=0x007 и определяет модель МАП.
func detectMapMode(ctx context.Context, r cellReader) (string, error) {
	cells, errs := r.ReadCells(ctx, []uint16{mapCellDevOpt})
	if len(errs) > 0 {
		return "", fmt.Errorf("определение модели: %s", strings.Join(errs, "; "))
	}
	v, ok := cells[mapCellDevOpt]
	if !ok {
		return "", fmt.Errorf("определение модели: ячейка 0x%03X недоступна", mapCellDevOpt)
	}
	if v == 3 {
		return mapModeTitanator, nil
	}
	return mapModeDominator, nil
}

// mapBit — битовое поле ячейки.
type mapBit struct {
	Bit  int    `json:"bit"`
	Name string `json:"name"`
}

// mapParamSpec — дескриптор одного параметра (ячейки) из каталога.
type mapParamSpec struct {
	Key    string            `json:"key"`
	Cell   string            `json:"cell"`
	Addr   uint16            `json:"addr"`
	Width  int               `json:"width"`
	Kind   string            `json:"kind"`   // ram|eeprom
	Access string            `json:"access"` // ro|rw
	Name   string            `json:"name"`   // русское имя
	Group  string            `json:"group"`  // логический блок
	Modes  []string          `json:"modes"`  // titanator|dominator
	Unit   string            `json:"unit"`
	Scale  float64           `json:"scale"`
	Offset float64           `json:"offset"`
	Min    *float64          `json:"min"`
	Max    *float64          `json:"max"`
	Desc   string            `json:"desc"`
	Enum   map[string]string `json:"enum"`
	Bits   []mapBit          `json:"bits"`
	// Order задаёт порядок байт для Width==2: "hl" (первая ячейка — старший
	// байт, по умолчанию) или "lh" (первая ячейка — младший байт).
	Order string `json:"order"`
	// Format задаёт специальное представление значения: "" — обычное,
	// "ver" — версия «целая.дробная» (младшие 5 бит — целая, старшие 3 — дробная).
	Format string `json:"format"`
	// Fields описывает разбиение байта на несколько именованных контролов
	// (например, версия + флаг). Запись всё равно идёт одним числом.
	Fields []mapField `json:"fields"`
	// Link — связанная ячейка расширения `_dop`, читается/пишется вместе с
	// основной по формуле Kind.
	Link *mapLink `json:"link,omitempty"`
	// HighMask маскирует все байты слова, кроме младшего (для пар L/H, где
	// старший бит — признак ошибки, напр. BMS/MPPT).
	HighMask byte `json:"high_mask,omitempty"`
	// Columns — число колонок для радиогруппы/набора полей (>1).
	Columns int `json:"columns,omitempty"`
	// ShowValue — показывать числовой код под радиокнопками (только чтение).
	ShowValue bool `json:"show_value,omitempty"`
	// EnumByMode — перечисления, зависящие от модели МАП (mode -> варианты).
	EnumByMode map[string]map[string]string `json:"enum_by_mode,omitempty"`
}

// mapLink — дополнительная ячейка, расширяющая основную.
type mapLink struct {
	Addr uint16 `json:"addr"`
	Kind string `json:"kind"` // u16_hi | uacc10 | pow2
}

// mapField — именованное битовое поле внутри байта.
type mapField struct {
	Label  string            `json:"label"`
	Lsb    int               `json:"lsb"`
	Bits   int               `json:"bits"`
	Offset float64           `json:"offset,omitempty"`
	Enum   map[string]string `json:"enum,omitempty"`
}

type mapSettingsCatalog struct {
	Source string         `json:"source"`
	Title  string         `json:"title"`
	Params []mapParamSpec `json:"params"`
}

var (
	mapSettingsCatalogOnce sync.Once
	mapSettingsCatalogData *mapSettingsCatalog
	mapSettingsCatalogErr  error
)

// loadMapSettingsCatalog разбирает встроенный JSON-каталог (единожды).
func loadMapSettingsCatalog() (*mapSettingsCatalog, error) {
	mapSettingsCatalogOnce.Do(func() {
		var c mapSettingsCatalog
		if err := json.Unmarshal(mapSettingsCatalogJSON, &c); err != nil {
			mapSettingsCatalogErr = fmt.Errorf("map-settings: разбор каталога: %w", err)
			return
		}
		for i := range c.Params {
			p := &c.Params[i]
			if p.Width <= 0 {
				p.Width = 1
			}
			if p.Scale == 0 {
				p.Scale = 1
			}
			if p.Access == "" {
				p.Access = "ro"
			}
		}
		c.Params = normalizeCatalogPairs(c.Params)
		mapSettingsCatalogData = &c
	})
	return mapSettingsCatalogData, mapSettingsCatalogErr
}

// cellPairRole разбирает имя ячейки на базовое имя пары и роль байта:
// 'L' — младший байт (_L/_VL/_L[n]), 'H' — старший (_H/_VH/_H[n]), 0 — не пара.
func cellPairRole(cell string) (string, byte) {
	// Серийный номер: 0 — младший байт, 1 — старший (один 16-битный номер).
	if cell == "_SerialNum0" {
		return "_SerialNum", 'L'
	}
	if cell == "_SerialNum1" {
		return "_SerialNum", 'H'
	}
	head, indexed := cell, ""
	if i := strings.LastIndexByte(cell, '['); i >= 0 {
		head, indexed = cell[:i], cell[i:]
	}
	switch {
	case strings.HasSuffix(head, "_VL"):
		return head[:len(head)-3] + indexed, 'L'
	case strings.HasSuffix(head, "_L"):
		return head[:len(head)-2] + indexed, 'L'
	case strings.HasSuffix(head, "_VH"):
		return head[:len(head)-3] + indexed, 'H'
	case strings.HasSuffix(head, "_H"):
		return head[:len(head)-2] + indexed, 'H'
	}
	return "", 0
}

// normalizeCatalogPairs приводит парные ячейки _L/_H (_VL/_VH) к одному
// параметру width==2 с явным порядком байт. Идемпотентна: если каталог уже
// нормализован, ничего не меняет.
//
// Правила:
//   - если младший и старший байты на СОСЕДНИХ адресах — объединяем в один
//     width==2 (addr — меньший, order "lh" если младший ниже по адресу, иначе
//     "hl"), партнёр удаляется;
//   - если адреса НЕ соседние — оба остаются width==1 (объединять нельзя);
//   - width==2 без order и без пары — order по роли имени; при пересечении с
//     соседним адресом — width==1.
func normalizeCatalogPairs(params []mapParamSpec) []mapParamSpec {
	type pair struct{ low, high int }
	byBase := map[string]*pair{}
	for i := range params {
		base, role := cellPairRole(params[i].Cell)
		if role == 0 {
			continue
		}
		e := byBase[base]
		if e == nil {
			e = &pair{low: -1, high: -1}
			byBase[base] = e
		}
		if role == 'L' {
			e.low = i
		} else {
			e.high = i
		}
	}
	removed := make([]bool, len(params))
	for _, e := range byBase {
		if e.low < 0 || e.high < 0 {
			continue
		}
		aL, aH := params[e.low].Addr, params[e.high].Addr
		if aL > aH {
			aL, aH = aH, aL
		}
		if aH-aL != 1 {
			params[e.low].Width = 1
			params[e.high].Width = 1
			continue
		}
		if params[e.low].Addr < params[e.high].Addr {
			params[e.low].Width = 2
			params[e.low].Order = "lh"
			removed[e.high] = true
		} else {
			params[e.high].Width = 2
			params[e.high].Order = "hl"
			removed[e.low] = true
		}
	}
	// Второй проход: оставшиеся width==2 без order; при пересечении — width==1.
	addrSet := map[uint16]bool{}
	for i := range params {
		if !removed[i] {
			addrSet[params[i].Addr] = true
		}
	}
	for i := range params {
		if removed[i] {
			continue
		}
		p := &params[i]
		if p.Width == 2 {
			// Пересечение со следующей ячейкой недопустимо: только один байт.
			if addrSet[p.Addr+1] {
				p.Width = 1
				p.Order = ""
				continue
			}
			if _, role := cellPairRole(p.Cell); role == 'L' {
				p.Order = "lh"
			} else if role == 'H' {
				p.Order = "hl"
			} else if p.Order == "" {
				p.Order = "hl"
			}
		}
	}
	out := make([]mapParamSpec, 0, len(params))
	seen := map[string]bool{}
	for i := range params {
		if removed[i] {
			continue
		}
		if seen[params[i].Key] {
			params[i].Key = fmt.Sprintf("%s_%03x", params[i].Key, params[i].Addr)
		}
		seen[params[i].Key] = true
		out = append(out, params[i])
	}
	return out
}

// paramInMode сообщает, актуален ли параметр для выбранного режима.
func paramInMode(p mapParamSpec, mode string) bool {
	if len(p.Modes) == 0 {
		return true
	}
	for _, m := range p.Modes {
		m = strings.ToLower(strings.TrimSpace(m))
		if m == mode || m == "both" || m == "all" {
			return true
		}
	}
	return false
}

// normalizeMapMode приводит режим к каноническому виду.
func normalizeMapMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case mapModeTitanator, "титанатор", "titan":
		return mapModeTitanator
	default:
		return mapModeDominator
	}
}

// --- Чтение снимка ячеек ---

// neededAddrs возвращает отсортированный уникальный список байтовых адресов,
// необходимых для параметров (для Width==2 — плюс следующий байт).
func neededAddrs(params []mapParamSpec) []uint16 {
	set := map[uint16]struct{}{}
	for _, p := range params {
		for i := 0; i < p.Width; i++ {
			set[p.Addr+uint16(i)] = struct{}{}
		}
		if p.Link != nil {
			set[p.Link.Addr] = struct{}{}
			if p.Link.Kind == "uacc10" || p.Link.Kind == "uacc" {
				set[mapCellUACC] = struct{}{}
			}
		}
	}
	out := make([]uint16, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// readCellRanges читает ячейки (по списку адресов) блоками. Соседние адреса
// (и близкие с зазором до 16 байт) объединяются в один Modbus-запрос; блок
// режется по 240 байт (лимит ReadRegisters — 120 слов). Возвращает карту
// адрес→байт и список ошибок по блокам (частичный снимок допускается).
func readCellRanges(ctx context.Context, c *Client, addrs []uint16) (map[uint16]byte, []string) {
	cells := make(map[uint16]byte, len(addrs))
	var errs []string
	if len(addrs) == 0 {
		return cells, errs
	}
	const maxBytes = 240
	const gapTolerance = 16
	// Группируем в диапазоны [start,end] с допустимым зазором.
	type span struct{ start, end int }
	var spans []span
	start := int(addrs[0])
	end := start
	for _, a := range addrs[1:] {
		x := int(a)
		if x-end-1 <= gapTolerance {
			end = x
			continue
		}
		spans = append(spans, span{start, end})
		start, end = x, x
	}
	spans = append(spans, span{start, end})

	for _, sp := range spans {
		for from := sp.start; from <= sp.end; {
			to := sp.end
			if to-from+1 > maxBytes {
				to = from + maxBytes - 1
			}
			n := to - from + 1
			count := uint16((n + 1) / 2)
			raw, err := c.ReadRegisters(ctx, uint16(from), count)
			if err != nil {
				errs = append(errs, fmt.Sprintf("чтение 0x%03X..0x%03X: %v", from, to, err))
				from = to + 1
				continue
			}
			for i := 0; i < n && i < len(raw); i++ {
				cells[uint16(from+i)] = raw[i]
			}
			from = to + 1
		}
	}
	return cells, errs
}

// rawValue собирает сырое значение параметра из карты ячеек.
func rawValue(p mapParamSpec, cells map[uint16]byte) (int, bool) {
	if p.Link != nil {
		return linkedRaw(p, cells)
	}
	b0, ok := cells[p.Addr]
	if !ok {
		return 0, false
	}
	if p.Width < 2 {
		return int(b0), true
	}
	raw := 0
	for i := 0; i < p.Width; i++ {
		b, ok := cells[p.Addr+uint16(i)]
		if !ok {
			return 0, false
		}
		if p.HighMask != 0 {
			// Младший байт не маскируем, остальные — да.
			least := (p.Order == "lh" && i == 0) || (p.Order != "lh" && i == p.Width-1)
			if !least {
				b &= p.HighMask
			}
		}
		if p.Order == "lh" {
			raw |= int(b) << (8 * i)
		} else {
			raw = raw<<8 | int(b)
		}
	}
	return raw, true
}

// linkedRaw объединяет основную ячейку и её `_dop`-расширение.
func linkedRaw(p mapParamSpec, cells map[uint16]byte) (int, bool) {
	main, ok := cells[p.Addr]
	if !ok {
		return 0, false
	}
	dop, ok := cells[p.Link.Addr]
	if !ok {
		return 0, false
	}
	switch p.Link.Kind {
	case "uacc":
		return int(main), true
	case "u16_hi", "cacc":
		return int(main) | int(dop)<<8, true
	case "uacc10":
		u, ok := cells[mapCellUACC]
		if !ok {
			return 0, false
		}
		return int(main)<<uint(u) | int(dop), true
	case "pow2":
		factor := 1
		if dop == 1 {
			factor = 2
		}
		return int(main) * factor, true
	}
	return 0, false
}

// linkedValue возвращает «сырое» (объединённое) и отображаемое значения
// связанной пары. Для cacc отображаемое = (combined*25)>>UACC (документ).
func linkedValue(p mapParamSpec, cells map[uint16]byte) (int, float64, bool) {
	main, ok := cells[p.Addr]
	if !ok {
		return 0, 0, false
	}
	dop, ok := cells[p.Link.Addr]
	if !ok {
		return 0, 0, false
	}
	switch p.Link.Kind {
	case "uacc":
		u, ok := cells[mapCellUACC]
		if !ok {
			return 0, 0, false
		}
		if u > 7 {
			u = 7
		}
		return int(main), float64(int(main)<<uint(u)) / 10, true
	case "u16_hi":
		c := int(main) | int(dop)<<8
		return c, float64(c), true
	case "cacc":
		c := int(main) | int(dop)<<8
		u, ok := cells[mapCellUACC]
		if !ok {
			return 0, 0, false
		}
		if u > 7 {
			u = 7
		}
		return c, float64((c * 25) >> uint(u)), true
	case "i16_hi7":
		c := int(main) + int(dop&0x7F)<<8
		v := float64(c) / 10
		if dop&0x80 != 0 {
			v = -v
		}
		return c, v, true
	case "uacc10":
		u, ok := cells[mapCellUACC]
		if !ok {
			return 0, 0, false
		}
		if u > 7 {
			u = 7
		}
		c := int(main)<<uint(u) | int(dop)
		return c, float64(c) / 10, true
	case "pow2":
		factor := 1
		if dop == 1 {
			factor = 2
		}
		c := int(main) * factor
		return c, float64(c) * 100, true
	}
	return 0, 0, false
}

// linkedBytes раскладывает значение связанной пары в байты основной ячейки и
// `_dop`. uacc — текущий код напряжения АКБ (_UACC) для kind=uacc10.
func linkedBytes(p mapParamSpec, value float64, uacc int) (byte, byte, error) {
	if p.Scale == 0 {
		return 0, 0, fmt.Errorf("нулевой масштаб")
	}
	logical := int(math.Round((value - p.Offset) / p.Scale))
	if logical < 0 && p.Link.Kind != "i16_hi7" {
		return 0, 0, fmt.Errorf("значение %g вне допустимого", value)
	}
	switch p.Link.Kind {
	case "u16_hi":
		if logical > 65535 {
			return 0, 0, fmt.Errorf("значение %g больше слова", value)
		}
		return byte(logical), byte(logical >> 8), nil
	case "cacc":
		// Обратная формула: combined = Ah * 2^UACC / 25; main+dop<<8.
		if uacc < 0 || uacc > 7 {
			return 0, 0, fmt.Errorf("некорректный код напряжения АКБ: %d", uacc)
		}
		combined := int(math.Round((value - p.Offset) * float64(int(1)<<uint(uacc)) / 25.0))
		if combined < 0 || combined > 65535 {
			return 0, 0, fmt.Errorf("значение %g не представимо", value)
		}
		main := combined & 0xFF
		dop := (combined >> 8) & 0xFF
		if main == 255 {
			// 255 закрывает пункт меню — уменьшаем до 254.
			main = 254
		}
		return byte(main), byte(dop), nil
	case "uacc":
		// Однобайтовое значение со сдвигом по UACC: U(В)=(raw<<UACC)/10.
		if uacc < 0 || uacc > 7 {
			return 0, 0, fmt.Errorf("некорректный код напряжения АКБ: %d", uacc)
		}
		raw := int(math.Round(value*10)) >> uint(uacc)
		if raw < 0 || raw > 255 {
			return 0, 0, fmt.Errorf("значение %g не представимо", value)
		}
		return byte(raw), byte(raw), nil
	case "i16_hi7":
		// Фазный ток: (L + (H&0x7F)*256)/10; бит7 H — знак (заряд).
		logical = int(math.Round(math.Abs(value) * 10))
		if logical > 0x7FFF {
			return 0, 0, fmt.Errorf("значение %g не представимо", value)
		}
		main := byte(logical & 0xFF)
		dop := byte((logical >> 8) & 0x7F)
		if value < 0 {
			dop |= 0x80
		}
		return main, dop, nil
	case "uacc10":
		if uacc < 0 || uacc > 7 {
			return 0, 0, fmt.Errorf("некорректный код напряжения АКБ: %d", uacc)
		}
		main := logical >> uint(uacc)
		dop := logical - (main << uint(uacc))
		if main > 255 || dop > 255 {
			return 0, 0, fmt.Errorf("значение %g не представимо (main=%d dop=%d)", value, main, dop)
		}
		return byte(main), byte(dop), nil
	case "pow2":
		if logical <= 255 {
			return byte(logical), 0, nil
		}
		if logical%2 == 0 && logical/2 <= 255 {
			return byte(logical / 2), 1, nil
		}
		return 0, 0, fmt.Errorf("мощность %g не представима", value)
	}
	return 0, 0, fmt.Errorf("неизвестный тип связи %q", p.Link.Kind)
}

// displayValue переводит сырое значение в отображаемое (scale/offset).
func displayValue(p mapParamSpec, raw int) float64 {
	return mapRound3(float64(raw)*p.Scale + p.Offset)
}

// splitRaw раскладывает отображаемое значение в 1–2 байта ячейки.
func splitRaw(p mapParamSpec, value float64) ([]byte, error) {
	if p.Scale == 0 {
		return nil, fmt.Errorf("нулевой масштаб")
	}
	if p.Width > 2 {
		return nil, fmt.Errorf("запись %d-байтной ячейки не поддержана", p.Width)
	}
	raw := int(math.Round((value - p.Offset) / p.Scale))
	if raw < 0 {
		return nil, fmt.Errorf("значение %g вне допустимого (получается %d)", value, raw)
	}
	if p.Width < 2 {
		if raw > 255 {
			return nil, fmt.Errorf("значение %g больше байта", value)
		}
		return []byte{byte(raw)}, nil
	}
	if raw > 65535 {
		return nil, fmt.Errorf("значение %g больше слова", value)
	}
	hi, lo := byte(raw>>8), byte(raw)
	if p.Order == "lh" {
		return []byte{lo, hi}, nil
	}
	return []byte{hi, lo}, nil
}

// enumText возвращает текстовую расшифровку значения (enum или битовые поля).
func enumText(p mapParamSpec, raw int) string {
	if len(p.Enum) > 0 {
		if s, ok := p.Enum[strconv.Itoa(raw)]; ok {
			return s
		}
	}
	if len(p.Bits) > 0 {
		var on []string
		for _, b := range p.Bits {
			if raw&(1<<uint(b.Bit)) != 0 {
				on = append(on, b.Name)
			}
		}
		return strings.Join(on, "; ")
	}
	return ""
}

func mapRound3(v float64) float64 { return math.Round(v*1000) / 1000 }

// mapOption — вариант перечислимого параметра (значение + подпись из документа).
type mapOption struct {
	Value float64 `json:"value"`
	Label string  `json:"label"`
}

// mapSettingView — параметр в ответе API.
type mapSettingView struct {
	Key       string          `json:"key"`
	Cell      string          `json:"cell"`
	Addr      string          `json:"addr"`
	Name      string          `json:"name"`
	Unit      string          `json:"unit"`
	Kind      string          `json:"kind"`
	Access    string          `json:"access"`
	Writable  bool            `json:"writable"`
	Danger    bool            `json:"danger,omitempty"`
	Format    string          `json:"format,omitempty"`
	Fields    []mapField      `json:"fields,omitempty"`
	Columns   int             `json:"columns,omitempty"`
	ShowValue bool            `json:"show_value,omitempty"`
	Flag      *mapSettingView `json:"flag,omitempty"`
	Value     *float64        `json:"value"`
	Raw       *int            `json:"raw"`
	Text      string          `json:"text,omitempty"`
	Min       *float64        `json:"min,omitempty"`
	Max       *float64        `json:"max,omitempty"`
	Scale     float64         `json:"scale"`
	Offset    float64         `json:"offset"`
	Desc      string          `json:"desc,omitempty"`
	Help      string          `json:"help,omitempty"`
	Error     string          `json:"error,omitempty"`
	Options   []mapOption     `json:"options,omitempty"`
	Bits      []mapBit        `json:"bits,omitempty"`
}

// dangerKeys — ячейки (по ключу), изменение которых опасно для оборудования.
// В UI такие параметры по умолчанию не редактируются; запись включается
// отдельной кнопкой на время сессии страницы.
var dangerKeys = map[string]bool{
	"pow": true, "uacc": true, "devopt": true,
	"fuacc_korr": true, "pow_korr": true, "i_chage_korr": true,
	"lcd_cichargeend":   true,
	"tft_cichargesoc95": true,
	"deluchargeend":     true,
	"tft_soc_discharge": true, "tft_soc_startcharge": true, "tft_soc_dizstart": true,
	"powaccnom": true, "lcd_dizelmaxpow": true,
	"lcd_umap220_need": true, "lcd_netalg": true,
	"lcd_unetup": true, "lcd_unetdown": true, "lcd_unet2up": true, "lcd_unet2down": true,
	"lcd_map_ifaze": true, "lcd_map_sync": true,
	"lcd_slavemapnum": true, "bms_num": true,
	"no3fazemotor": true, "mask_dev_on": true, "avr_on": true,
	"grid_52hz": true, "syncdiz_plat": true,
	"lcd_netupload": true, "lcd_netupeco": true, "lcd_net2": true,
	"lcd_netdizel": true, "netupeco_net2_off": true, "lcd_sensload": true,
	"frozenuaccafterdischage": true, "uaccafterdischage": true,
	// Версии/служебные ячейки: запись может нарушить работу.
	"ram_end_l": true, "verplatpic": true, "verplatpowdop": true,
	"vertest": true, "verpow": true, "device": true,
	"serialnum0": true, "serialnum2": true, "serialnum3": true,
	"verpo": true, "verplatnet": true,
}

// isDangerKey сообщает, относится ли параметр к опасным. normalizeCatalogPairs
// может переименовать дублирующийся ключ, добавив суффикс "_%03x"; базовый ключ
// при этом остаётся префиксом, поэтому проверяем и его.
func isDangerKey(key string) bool {
	if dangerKeys[key] {
		return true
	}
	if len(key) > 4 && key[len(key)-4] == '_' {
		base, suf := key[:len(key)-4], key[len(key)-3:]
		for i := 0; i < len(suf); i++ {
			c := suf[i]
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
		return dangerKeys[base]
	}
	return false
}

// paramOptions строит список вариантов для перечислимого параметра: значение —
// в отображаемых единицах (с учётом scale/offset), подпись — из документа.
// paramEnum возвращает перечисление параметра с учётом модели МАП.
func paramEnum(p mapParamSpec, mode string) map[string]string {
	if e, ok := p.EnumByMode[mode]; ok && len(e) > 0 {
		return e
	}
	return p.Enum
}

func paramOptions(p mapParamSpec, mode string) []mapOption {
	enum := paramEnum(p, mode)
	if len(enum) == 0 {
		return nil
	}
	keys := make([]int, 0, len(enum))
	for k := range enum {
		if n, err := strconv.Atoi(k); err == nil {
			keys = append(keys, n)
		}
	}
	sort.Ints(keys)
	opts := make([]mapOption, 0, len(keys))
	for _, k := range keys {
		text := enum[strconv.Itoa(k)]
		opts = append(opts, mapOption{
			Value: mapRound3(float64(k)*p.Scale + p.Offset),
			Label: strconv.Itoa(k) + " — " + text,
		})
	}
	return opts
}

// paramHelp собирает текст подсказки из документации: описание ячейки, затем
// (при наличии) единица/формула, расшифровки значений (enum) и битовые поля.
// Имя параметра в подсказку не включается — оно и так в колонке «Параметр».
func paramHelp(p mapParamSpec, mode string) string {
	var b strings.Builder
	if p.Desc != "" {
		b.WriteString(p.Desc)
	}
	if p.Unit != "" || p.Scale != 1 || p.Offset != 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "Единица: %s", p.Unit)
		if p.Scale != 1 || p.Offset != 0 {
			fmt.Fprintf(&b, "; отображение = значение×%g%+g", p.Scale, p.Offset)
		}
	}
	if enum := paramEnum(p, mode); len(enum) > 0 {
		keys := make([]int, 0, len(enum))
		for k := range enum {
			if n, err := strconv.Atoi(k); err == nil {
				keys = append(keys, n)
			}
		}
		sort.Ints(keys)
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Значения:")
		for _, k := range keys {
			fmt.Fprintf(&b, "\n• %d — %s", k, enum[strconv.Itoa(k)])
		}
	}
	if len(p.Bits) > 0 {
		bits := append([]mapBit(nil), p.Bits...)
		sort.Slice(bits, func(i, j int) bool { return bits[i].Bit < bits[j].Bit })
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Биты:")
		for _, bit := range bits {
			fmt.Fprintf(&b, "\n• бит %d — %s", bit.Bit, bit.Name)
		}
	}
	return b.String()
}

type mapSettingsGroup struct {
	Name   string           `json:"name"`
	Params []mapSettingView `json:"params"`
}

type mapSettingsActionView struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Desc         string `json:"desc"`
	Confirm      bool   `json:"confirm"`
	ConfirmCount int    `json:"confirmCount,omitempty"`
}

// mapSettingsSnapshot — полный ответ GET /api/map-settings.
type mapSettingsSnapshot struct {
	Mode     string                  `json:"mode"`
	IP       string                  `json:"ip"`
	Port     int                     `json:"port"`
	Unit     int                     `json:"unit"`
	ReadAt   string                  `json:"read_at"`
	Settings []mapSettingsGroup      `json:"settings"`
	Monitor  []mapSettingsGroup      `json:"monitor"`
	Actions  []mapSettingsActionView `json:"actions"`
	Errors   []string                `json:"errors,omitempty"`
}

// buildSnapshot формирует снимок: настройки (rw) и мониторинг (ro) по группам.
func buildSnapshot(mode string, params []mapParamSpec, cells map[uint16]byte, readErrs []string) *mapSettingsSnapshot {
	snap := &mapSettingsSnapshot{
		Mode:    mode,
		ReadAt:  time.Now().Format(time.RFC3339),
		Actions: mapSettingsActionsView(),
		Errors:  readErrs,
	}
	snap.Settings = []mapSettingsGroup{}
	snap.Monitor = []mapSettingsGroup{}
	add := func(section *[]mapSettingsGroup, index map[string]int, group string) int {
		if idx, ok := index[group]; ok {
			return idx
		}
		*section = append(*section, mapSettingsGroup{Name: group})
		idx := len(*section) - 1
		index[group] = idx
		return idx
	}
	setIdx := map[string]int{}
	monIdx := map[string]int{}
	flags := map[string]mapSettingView{}
	for _, p := range params {
		if !paramInMode(p, mode) {
			continue
		}
		v := settingView(p, cells, mode)
		// Ячейки-флаги, «прикреплённые» к другому параметру, не выводятся
		// отдельной строкой — их контрол показывается в строке цели.
		if _, isFlag := paramFlags[p.Key]; isFlag {
			flags[p.Key] = v
			continue
		}
		if p.Access == "rw" {
			i := add(&snap.Settings, setIdx, p.Group)
			snap.Settings[i].Params = append(snap.Settings[i].Params, v)
		} else {
			i := add(&snap.Monitor, monIdx, p.Group)
			snap.Monitor[i].Params = append(snap.Monitor[i].Params, v)
		}
	}
	// Прикрепляем виды флагов к целевым параметрам.
	for flagKey, targetKey := range paramFlags {
		fv, ok := flags[flagKey]
		if !ok {
			continue
		}
		attach := func(groups []mapSettingsGroup) bool {
			for i := range groups {
				for j := range groups[i].Params {
					if groups[i].Params[j].Key == targetKey {
						f := fv
						groups[i].Params[j].Flag = &f
						return true
					}
				}
			}
			return false
		}
		if !attach(snap.Settings) {
			attach(snap.Monitor)
		}
	}
	return snap
}

// noVerifyKeys — командные (write-only) ячейки: записанное значение в них не
// хранится, поэтому чтение-обратно не показательно и верификацию пропускаем
// (успех определяется ответом транспорта).
var noVerifyKeys = map[string]bool{
	"status_reledop": true,
	"put_eeprom":     true,
}

// paramFlags — ячейки-флаги, отображаемые в строке другого параметра
// (ключ флага → ключ целевого параметра). Сейчас пусто: флаг
// `_FrozenUAccAfterDisChage` выводится отдельной строкой над `_UAccAfterDisChage`.
var paramFlags = map[string]string{}

// settingView собирает представление одного параметра по прочитанным ячейкам.
func settingView(p mapParamSpec, cells map[uint16]byte, mode string) mapSettingView {
	v := mapSettingView{
		Key: p.Key, Cell: p.Cell, Addr: fmt.Sprintf("0x%03X", p.Addr),
		Name: p.Name, Unit: p.Unit, Kind: p.Kind, Access: p.Access,
		Writable: p.Access == "rw", Danger: isDangerKey(p.Key),
		Format: p.Format, Fields: p.Fields, Columns: p.Columns, ShowValue: p.ShowValue,
		Min: p.Min, Max: p.Max, Desc: p.Desc,
		Scale: p.Scale, Offset: p.Offset,
		Help: paramHelp(p, mode),
	}
	if p.Link != nil {
		// Связанная пара «основная + _dop»: значение считается по формуле.
		if raw, val, ok := linkedValue(p, cells); ok {
			r := raw
			v.Raw = &r
			v.Value = &val
		} else {
			v.Error = "нет данных"
		}
	} else {
		raw, ok := rawValue(p, cells)
		if !ok {
			v.Error = "нет данных"
		} else {
			r := raw
			val := displayValue(p, raw)
			if p.Format == "freq" {
				// Частота: F(Гц)=6250/ThFMAP (ПО >= 17.0).
				val = 0
				if raw > 0 {
					val = math.Round(625000.0/float64(raw)) / 100
				}
			}
			if p.Format == "days12" {
				// Дни: значение/12; код 255 (не задано) показываем как есть.
				if raw == 255 {
					val = 255
				} else {
					val = math.Round(float64(raw)/12*100) / 100
				}
			}
			v.Raw = &r
			v.Value = &val
			v.Text = enumText(p, raw)
		}
	}
	// Перечисления и биты отдаём и для ro-параметров: клиент показывает их
	// как неактивные (read-only) контролы, чтобы смысл значения был виден.
	if opts := paramOptions(p, mode); len(opts) > 0 {
		v.Options = opts
	}
	if len(p.Bits) > 0 {
		v.Bits = p.Bits
	}
	return v
}

// readMapSettingView перечитывает один параметр по ключу.
func readMapSettingView(ctx context.Context, r cellReader, mode, key string) (*mapSettingView, error) {
	cat, err := loadMapSettingsCatalog()
	if err != nil {
		return nil, err
	}
	var spec mapParamSpec
	found := false
	for _, p := range cat.Params {
		if p.Key == key && paramInMode(p, mode) {
			spec, found = p, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("неизвестный параметр %q", key)
	}
	cells, errs := r.ReadCells(ctx, neededAddrs([]mapParamSpec{spec}))
	v := settingView(spec, cells, mode)
	if v.Error == "нет данных" && len(errs) > 0 {
		v.Error = strings.Join(errs, "; ")
	}
	return &v, nil
}

// readMapSettingsSnapshot читает все параметры (для режима) с МАП.
func readMapSettingsSnapshot(ctx context.Context, r cellReader, mode string) (*mapSettingsSnapshot, error) {
	cat, err := loadMapSettingsCatalog()
	if err != nil {
		return nil, err
	}
	params := make([]mapParamSpec, 0, len(cat.Params))
	for _, p := range cat.Params {
		if paramInMode(p, mode) {
			params = append(params, p)
		}
	}
	cells, errs := r.ReadCells(ctx, neededAddrs(params))
	return buildSnapshot(mode, params, cells, errs), nil
}

// --- Запись ---

// applyMapSettings применяет изменения (только изменённые параметры) со служебным
// обрамлением и верификацией. changes: key → новое отображаемое значение.
func applyMapSettings(ctx context.Context, conn cellConn, mode string, changes map[string]float64, allowDanger bool) (map[string]string, error) {
	cat, err := loadMapSettingsCatalog()
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]mapParamSpec, len(cat.Params))
	for _, p := range cat.Params {
		if paramInMode(p, mode) {
			byKey[p.Key] = p
		}
	}
	// Для связанных пар uacc10 нужен текущий код напряжения АКБ (_UACC).
	needUacc := false
	for key := range changes {
		if p, ok := byKey[key]; ok && p.Link != nil && (p.Link.Kind == "uacc10" || p.Link.Kind == "uacc") {
			needUacc = true
			break
		}
	}
	uacc := 0
	if needUacc {
		cells, errs := conn.ReadCells(ctx, []uint16{mapCellUACC})
		u, ok := cells[mapCellUACC]
		if !ok || len(errs) > 0 {
			return nil, fmt.Errorf("чтение _UACC для связанных ячеек: %s", strings.Join(errs, "; "))
		}
		uacc = int(u)
	}
	var writes []cellWrite
	// Соответствие адрес → исходный ключ параметра (для понятных сообщений
	// верификации и для связанных пар, у которых несколько ячеек).
	addrKey := map[uint16]string{}
	addWrite := func(addr uint16, b byte, key string) {
		writes = append(writes, cellWrite{addr, b})
		addrKey[addr] = key
	}
	// Командные (write-only) ячейки не читаются обратно для верификации.
	skipVerify := map[uint16]bool{}
	results := map[string]string{}
	for key, val := range changes {
		p, ok := byKey[key]
		if !ok {
			results[key] = "неизвестный параметр"
			continue
		}
		if p.Access != "rw" {
			results[key] = "параметр только для чтения"
			continue
		}
		// Опасные параметры записываются только при явном подтверждении
		// (allowDanger), которое клиент ставит после разблокировки в UI.
		if isDangerKey(key) && !allowDanger {
			results[key] = "опасный параметр: запись не подтверждена"
			continue
		}
		// Ячейки 0x000..0x004 — служебная область команд/идентификации: запись
		// настройкой недопустима (команды идут отдельным путём, см. actions).
		if p.Addr <= 4 {
			results[key] = "служебная ячейка — запись запрещена"
			continue
		}
		if p.Min != nil && val < *p.Min {
			results[key] = fmt.Sprintf("ниже минимума %g", *p.Min)
			continue
		}
		if p.Max != nil && val > *p.Max {
			results[key] = fmt.Sprintf("выше максимума %g", *p.Max)
			continue
		}
		if noVerifyKeys[key] {
			skipVerify[p.Addr] = true
		}
		if p.Link != nil {
			main, dop, lerr := linkedBytes(p, val, uacc)
			if lerr != nil {
				results[key] = lerr.Error()
				continue
			}
			writes = append(writes, cellWrite{p.Addr, main})
			addrKey[p.Addr] = key
			if p.Link.Addr != p.Addr {
				writes = append(writes, cellWrite{p.Link.Addr, dop})
				addrKey[p.Link.Addr] = key
			}
			if noVerifyKeys[key] {
				skipVerify[p.Link.Addr] = true
			}
			continue
		}
		data, err := splitRaw(p, val)
		if err != nil {
			results[key] = err.Error()
			continue
		}
		for i, b := range data {
			addWrite(p.Addr+uint16(i), b, key)
		}
	}
	if len(writes) == 0 {
		return results, nil
	}
	// Запись целиком через транспорт (он сам делает служебное обрамление,
	// а для Малины — отправляет mapd без стартовой/завершающей команды).
	if err := conn.WriteBytes(ctx, writes); err != nil {
		return results, err
	}
	// Чтение-обратно затронутых ячеек.
	addrs := make([]uint16, 0, len(writes))
	for _, w := range writes {
		addrs = append(addrs, w.Addr)
	}
	cells, errs := conn.ReadCells(ctx, uniqueU16(addrs))
	readErr := strings.Join(errs, "; ")
	for _, w := range writes {
		if skipVerify[w.Addr] {
			continue
		}
		key := addrKey[w.Addr]
		if key == "" {
			key = fmt.Sprintf("0x%03X", w.Addr)
		}
		if _, done := results[key]; done {
			continue
		}
		got, ok := cells[w.Addr]
		if !ok {
			// Ошибку обратного чтения нельзя считать успешной верификацией:
			// сообщаем о ней явно, с исходным ключом параметра.
			if readErr != "" {
				results[key] = "верификация: ошибка обратного чтения: " + readErr
			} else {
				results[key] = "верификация: нет данных обратного чтения"
			}
			continue
		}
		if got != w.Value {
			results[key] = fmt.Sprintf("верификация: получено %d, ждали %d", got, w.Value)
		}
	}
	return results, nil
}

func uniqueU16(in []uint16) []uint16 {
	seen := map[uint16]struct{}{}
	out := in[:0:0]
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// --- Управляющие воздействия ---

type mapAction struct {
	Key          string
	Name         string
	Desc         string
	Cmd          byte
	Confirm      bool
	ConfirmCount int // сколько раз переспросить (0/1 — один раз)
	RelayNum     int // 1..4 — переключатель реле (не обычная команда)
}

// mapSettingsActions — команды МАП (запись по адресу 0). Не являются изменением
// настроек; состав — по protocol_MAP_cells_2026_07_15.doc.
var mapSettingsActions = []mapAction{
	{Key: "on", Name: "Включить МАП", Desc: "Включить генерацию (ComMAP_ON)", Cmd: ComMAPON, Confirm: true},
	{Key: "off", Name: "Выключить МАП", Desc: "Выключить генерацию (ComMAP_OFF)", Cmd: ComMAPOFF, Confirm: true},
	{Key: "charge_on", Name: "Включить заряд", Desc: "Включить заряд от сети (ComMAP_ChargeON)", Cmd: ComMAPChargeON, Confirm: true},
	{Key: "charge_off", Name: "Выключить заряд", Desc: "Выключить заряд от сети (ComMAP_ChargeOFF)", Cmd: ComMAPChargeOFF, Confirm: true},
	{Key: "stat_reset", Name: "Сброс статистики", Desc: "Сбросить накопленную статистику (ComMAP_StatReset)", Cmd: ComMAPStatReset, Confirm: true},
	{Key: "disch_off", Name: "Запретить разряд", Desc: "Выключение генерации по полному разряду (ComMAP_DischOff)", Cmd: ComMAPDischOff, Confirm: true},
	{Key: "load_eeprom", Name: "Загрузить настройки из EEPROM", Desc: "Инициализация данных из EEPROM (ComMAP_Call_load_EEProm)", Cmd: ComMAPCallLoadEEProm, Confirm: false},
	{Key: "reset", Name: "Сброс контроллера", Desc: "Полная перезагрузка МАП — крайняя мера (ComMAP_Reset)", Cmd: ComMAPReset, Confirm: true, ConfirmCount: 3},
	// Переключатели доп. реле (одна кнопка вкл/выкл по состоянию).
	{Key: "relay1", Name: "Реле 1", Desc: "Включить/выключить доп. реле 1", Confirm: true, RelayNum: 1},
	{Key: "relay2", Name: "Реле 2", Desc: "Включить/выключить доп. реле 2", Confirm: true, RelayNum: 2},
	{Key: "relay3", Name: "Реле 3", Desc: "Включить/выключить доп. реле 3 (Титанатор)", Confirm: true, RelayNum: 3},
	{Key: "relay4", Name: "Реле 4", Desc: "Включить/выключить доп. реле 4 (Титанатор)", Confirm: true, RelayNum: 4},
}

// relayCountForMode — число доп. реле для модели.
func relayCountForMode(mode string) int {
	if normalizeMapMode(mode) == mapModeTitanator {
		return 4
	}
	return 2
}

// readRelayStates читает состояние доп. реле (1 — включено, 0 — выключено).
// Доступно при связи с Малиной (read_json.php).
func readRelayStates(ctx context.Context, conn cellConn, mode string) ([]int, error) {
	mc, ok := conn.(*malinaConn)
	if !ok {
		return nil, fmt.Errorf("состояние реле доступно только при связи через Малину")
	}
	b, err := mc.get(ctx, "/read_json.php?device=map")
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("разбор read_json: %w", err)
	}
	n := relayCountForMode(mode)
	states := make([]int, n)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("_Relay%d", i+1)
		if v, ok := m[key]; ok {
			if f, ok := toInt(v); ok {
				if f != 0 {
					states[i] = 1
				}
			}
		}
	}
	return states, nil
}

func mapSettingsActionsView() []mapSettingsActionView {
	out := make([]mapSettingsActionView, 0, len(mapSettingsActions))
	for _, a := range mapSettingsActions {
		if a.RelayNum > 0 {
			continue // реле выводятся отдельными переключателями
		}
		out = append(out, mapSettingsActionView{Key: a.Key, Name: a.Name, Desc: a.Desc, Confirm: a.Confirm, ConfirmCount: a.ConfirmCount})
	}
	return out
}

// runMapSettingsAction выполняет команду по ключу.
func runMapSettingsAction(ctx context.Context, conn cellConn, mode, key string) error {
	var cmd byte
	found := false
	relay := 0
	for _, a := range mapSettingsActions {
		if a.Key == key {
			cmd, found, relay = a.Cmd, true, a.RelayNum
			break
		}
	}
	if !found {
		return fmt.Errorf("неизвестное действие %q", key)
	}
	// Переключатель доп. реле: читаем состояния, инвертируем нужный бит в
	// _Status_RELEdop (0x586), сохраняя остальные, и пишем одним значением.
	if relay > 0 {
		if relay > relayCountForMode(mode) {
			return fmt.Errorf("реле %d недоступно для этой модели", relay)
		}
		states, err := readRelayStates(ctx, conn, mode)
		if err != nil {
			return err
		}
		var b byte
		for i, s := range states {
			if s != 0 {
				b |= 1 << uint(i)
			}
		}
		b ^= 1 << uint(relay-1)
		return conn.WriteBytes(ctx, []cellWrite{{0x586, b}})
	}
	// Сервисные команды, меняющие EEPROM (сброс статистики/загрузка), обрамляем
	// разрешением записи — как и обычную запись.
	if key == "stat_reset" || key == "load_eeprom" {
		if err := conn.WriteCommand(ctx, ComMAPEEPromWR); err != nil {
			return err
		}
	}
	return conn.WriteCommand(ctx, cmd)
}

// --- Текущее время (отдельная форма) ---

type mapTimeState struct {
	Hour   int    `json:"hour"`
	Minute int    `json:"minute"`
	Cell1  int    `json:"cell_time_cyr"`
	Cell2  int    `json:"cell_time_min"`
	Raw    string `json:"raw"`
}

// readMapTime читает текущее время МАП из ячеек _LCD_TimeCyr/_TimeCyr_MINUT.
func readMapTime(ctx context.Context, r cellReader) (*mapTimeState, error) {
	cells, errs := r.ReadCells(ctx, []uint16{mapCellTimeCyr, mapCellTimeMin})
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	a, ok1 := cells[mapCellTimeCyr]
	b, ok2 := cells[mapCellTimeMin]
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("ячейки времени недоступны")
	}
	hh := int(a) >> 3
	mm := (int(a)&0x07)*10 + int(b)%10
	return &mapTimeState{
		Hour: hh, Minute: mm, Cell1: int(a), Cell2: int(b),
		Raw: fmt.Sprintf("0x%03X=%d, 0x%03X=%d", mapCellTimeCyr, a, mapCellTimeMin, b),
	}, nil
}

// writeMapTime записывает время МАП (часы + десятки/единицы минут) с обрамлением.
func writeMapTime(ctx context.Context, conn cellConn, hour, minute int) error {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return fmt.Errorf("некорректное время %02d:%02d", hour, minute)
	}
	c1 := byte((hour << 3) | (minute / 10))
	c2 := byte(minute % 10)
	if err := conn.WriteBytes(ctx, []cellWrite{{mapCellTimeCyr, c1}, {mapCellTimeMin, c2}}); err != nil {
		return err
	}
	cells, _ := conn.ReadCells(ctx, []uint16{mapCellTimeCyr, mapCellTimeMin})
	if got, ok := cells[mapCellTimeCyr]; ok && got != c1 {
		return fmt.Errorf("верификация 0x%03X: получено %d, ждали %d", mapCellTimeCyr, got, c1)
	}
	if got, ok := cells[mapCellTimeMin]; ok && got != c2 {
		return fmt.Errorf("верификация 0x%03X: получено %d, ждали %d", mapCellTimeMin, got, c2)
	}
	return nil
}
