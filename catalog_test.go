// mapsettings
// Copyright (C) 2026  Aleksandr Galinskii
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"strings"
	"testing"
	"unicode"
)

// TestMapSettingsCatalogLoads проверяет, что встроенный каталог ячеек валиден:
// разбирается, содержит параметры, уникальные ключи, русские имена и группы.
func TestMapSettingsCatalogLoads(t *testing.T) {
	cat, err := loadMapSettingsCatalog()
	if err != nil {
		t.Fatalf("каталог не загрузился: %v", err)
	}
	if len(cat.Params) == 0 {
		t.Fatal("каталог пуст")
	}
	seen := map[string]bool{}
	addrSet := map[uint16]bool{}
	for _, p := range cat.Params {
		addrSet[p.Addr] = true
	}
	for i, p := range cat.Params {
		if p.Key == "" {
			t.Errorf("параметр #%d без key", i)
		}
		if seen[p.Key] {
			t.Errorf("дублирующийся key %q", p.Key)
		}
		seen[p.Key] = true
		if p.Name == "" {
			t.Errorf("%s: пустое имя", p.Key)
		}
		if !hasCyrillic(p.Name) {
			t.Errorf("%s: имя без кириллицы: %q", p.Key, p.Name)
		}
		if p.Group == "" {
			t.Errorf("%s: пустая группа", p.Key)
		}
		if p.Access != "ro" && p.Access != "rw" {
			t.Errorf("%s: неизвестный access %q", p.Key, p.Access)
		}
		if p.Scale == 0 {
			t.Errorf("%s: нулевой масштаб", p.Key)
		}
		if p.Width == 2 {
			if p.Order != "lh" && p.Order != "hl" {
				t.Errorf("%s: width=2 без корректного order (%q)", p.Key, p.Order)
			}
			if addrSet[p.Addr+1] {
				t.Errorf("%s: width=2 пересекается с отдельной ячейкой 0x%03X", p.Key, p.Addr+1)
			}
		}
	}
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// TestMapSettingsParamInMode проверяет фильтрацию параметров по режиму.
func TestMapSettingsParamInMode(t *testing.T) {
	p := mapParamSpec{}
	if !paramInMode(p, mapModeDominator) || !paramInMode(p, mapModeTitanator) {
		t.Fatal("параметр без modes должен быть виден в обоих режимах")
	}
	only := mapParamSpec{Modes: []string{"titanator"}}
	if !paramInMode(only, mapModeTitanator) {
		t.Fatal("titanator-only должен быть виден в Титанаторе")
	}
	if paramInMode(only, mapModeDominator) {
		t.Fatal("titanator-only не должен быть виден в Доминаторе")
	}
}

// TestMapSettingsSplitRaw проверяет разбор/сборку значений с учётом порядка байт.
func TestMapSettingsSplitRaw(t *testing.T) {
	p1 := mapParamSpec{Width: 1, Scale: 1}
	b, err := splitRaw(p1, 200)
	if err != nil || len(b) != 1 || b[0] != 200 {
		t.Fatalf("width1: %v %v", b, err)
	}
	// Порядок «старший-младший» (по умолчанию).
	ph := mapParamSpec{Addr: 10, Width: 2, Scale: 1}
	b, err = splitRaw(ph, 0x1234)
	if err != nil || len(b) != 2 || b[0] != 0x12 || b[1] != 0x34 {
		t.Fatalf("hl: %v %v", b, err)
	}
	if got, ok := rawValue(ph, map[uint16]byte{10: 0x12, 11: 0x34}); !ok || got != 0x1234 {
		t.Fatalf("rawValue hl = %d ok=%v", got, ok)
	}
	// Порядок «младший-старший».
	pl := mapParamSpec{Addr: 10, Width: 2, Scale: 1, Order: "lh"}
	b, err = splitRaw(pl, 0x1234)
	if err != nil || b[0] != 0x34 || b[1] != 0x12 {
		t.Fatalf("lh: %v %v", b, err)
	}
	if got, ok := rawValue(pl, map[uint16]byte{10: 0x34, 11: 0x12}); !ok || got != 0x1234 {
		t.Fatalf("rawValue lh = %d ok=%v", got, ok)
	}
}

// TestMapSettingsLinked проверяет связанные пары «основная + _dop».
func TestMapSettingsLinked(t *testing.T) {
	// uacc10: (main<<UACC)+dop, /10.
	p := mapParamSpec{Addr: 0x13D, Scale: 0.1, Link: &mapLink{Addr: 0x102, Kind: "uacc10"}}
	cells := map[uint16]byte{0x13D: 116, 0x102: 0, mapCellUACC: 2}
	if raw, ok := rawValue(p, cells); !ok || raw != 464 {
		t.Fatalf("uacc10 rawValue=%d ok=%v", raw, ok)
	}
	main, dop, err := linkedBytes(p, 46.4, 2)
	if err != nil || main != 116 || dop != 0 {
		t.Fatalf("uacc10 linkedBytes=%d,%d,%v", main, dop, err)
	}
	// u16_hi: main + dop<<8.
	p2 := mapParamSpec{Addr: 0x181, Scale: 25, Link: &mapLink{Addr: 0x101, Kind: "u16_hi"}}
	main, dop, err = linkedBytes(p2, 1900, 0)
	if err != nil || main != 76 || dop != 0 {
		t.Fatalf("u16_hi linkedBytes=%d,%d,%v", main, dop, err)
	}
	if raw, ok := rawValue(p2, map[uint16]byte{0x181: 76, 0x101: 1}); !ok || raw != 332 {
		t.Fatalf("u16_hi rawValue=%d ok=%v", raw, ok)
	}
	// cacc: Ah = (main+dop<<8)*25 >> UACC.
	pc := mapParamSpec{Addr: 0x181, Scale: 1, Link: &mapLink{Addr: 0x101, Kind: "cacc"}}
	if _, disp, ok := linkedValue(pc, map[uint16]byte{0x181: 76, 0x101: 0, mapCellUACC: 2}); !ok || disp != 475 {
		t.Fatalf("cacc linkedValue=%v ok=%v", disp, ok)
	}
	main, dop, err = linkedBytes(pc, 475, 2)
	if err != nil || main != 76 || dop != 0 {
		t.Fatalf("cacc linkedBytes=%d,%d,%v", main, dop, err)
	}
	// pow2: main*100, dop=1 удваивает.
	p3 := mapParamSpec{Addr: 0x168, Scale: 100, Link: &mapLink{Addr: 0x107, Kind: "pow2"}}
	main, dop, err = linkedBytes(p3, 16000, 0)
	if err != nil || main != 160 || dop != 0 {
		t.Fatalf("pow2 linkedBytes=%d,%d,%v", main, dop, err)
	}
	if main, dop, err = linkedBytes(p3, 51000, 0); err != nil || main != 255 || dop != 1 {
		t.Fatalf("pow2 max linkedBytes=%d,%d,%v", main, dop, err)
	}
}

// TestMapSettingsHighMask проверяет маску старшего байта (BMS/MPPT).
func TestMapSettingsHighMask(t *testing.T) {
	p := mapParamSpec{Addr: 0x480, Width: 2, Order: "lh", Scale: 0.01, HighMask: 0x7F}
	// Старший бит (0x80) в H должен отбрасываться: (0x80<<8)|0x56 → 0x0056.
	cells := map[uint16]byte{0x480: 0x56, 0x481: 0x80}
	if raw, ok := rawValue(p, cells); !ok || raw != 0x56 {
		t.Fatalf("high_mask raw=%d ok=%v", raw, ok)
	}
	// 342 = 3.42В.
	cells[0x480], cells[0x481] = 0x56, 0x01 // 0x0156 = 342
	if raw, ok := rawValue(p, cells); !ok || raw != 342 {
		t.Fatalf("high_mask raw=%d ok=%v", raw, ok)
	}
	if got := displayValue(p, 342); got != 3.42 {
		t.Fatalf("displayValue=%g", got)
	}
}

// TestMapSettingsSignedPair проверяет знаковую пару токов фаз (i16_hi7).
func TestMapSettingsSignedPair(t *testing.T) {
	p := mapParamSpec{Addr: 0x528, Scale: 0.1, Link: &mapLink{Addr: 0x529, Kind: "i16_hi7"}}
	// +30.0 A: L=0x2E(46? нет), считаем 300 → L=0x2C, H=0x01 → (44 + 256)/10 = 30.0
	_, v, ok := linkedValue(p, map[uint16]byte{0x528: 0x2C, 0x529: 0x01})
	if !ok || v != 30.0 {
		t.Fatalf("i16_hi7 +: v=%v ok=%v", v, ok)
	}
	// -30.0 A: знак в бите7 H.
	_, v, ok = linkedValue(p, map[uint16]byte{0x528: 0x2C, 0x529: 0x81})
	if !ok || v != -30.0 {
		t.Fatalf("i16_hi7 -: v=%v ok=%v", v, ok)
	}
	main, dop, err := linkedBytes(p, -30.0, 0)
	if err != nil || main != 0x2C || dop != 0x81 {
		t.Fatalf("i16_hi7 bytes=%02X,%02X,%v", main, dop, err)
	}
}

// TestMapSettingsNormalizeMode проверяет синонимы режимов.
func TestMapSettingsNormalizeMode(t *testing.T) {
	cases := map[string]string{
		"titanator": mapModeTitanator, "Титанатор": mapModeTitanator, "titan": mapModeTitanator,
		"dominator": mapModeDominator, "": mapModeDominator, "xyz": mapModeDominator,
	}
	for in, want := range cases {
		if got := normalizeMapMode(in); got != want {
			t.Errorf("normalizeMapMode(%q)=%q, ждали %q", in, got, want)
		}
	}
}

// TestMapSettingsHelp проверяет, что подсказка содержит описание и расшифровки,
// но не имя параметра.
func TestMapSettingsHelp(t *testing.T) {
	p := mapParamSpec{Name: "Напряжение АКБ", Cell: "_UACC", Addr: 0x405, Desc: "среднее напряжение",
		Enum: map[string]string{"0": "нет"}, Bits: []mapBit{{Bit: 0, Name: "флаг"}}}
	h := paramHelp(p, mapModeTitanator)
	for _, want := range []string{"среднее напряжение", "нет", "флаг"} {
		if !strings.Contains(h, want) {
			t.Errorf("подсказка не содержит %q: %q", want, h)
		}
	}
	if strings.Contains(h, "Напряжение АКБ") {
		t.Errorf("подсказка не должна содержать имя параметра: %q", h)
	}
}

// TestMapSettingsValidateTarget проверяет валидацию адреса/порта.
func TestMapSettingsValidateTarget(t *testing.T) {
	if _, err := validateMapTarget("dominator", "192.168.0.10", 502); err != nil {
		t.Fatalf("корректный адрес отвергнут: %v", err)
	}
	if _, err := validateMapTarget("dominator", "", 502); err == nil {
		t.Fatal("пустой IP должен отвергаться")
	}
	if _, err := validateMapTarget("dominator", "192.168.0.10", 0); err == nil {
		t.Fatal("порт 0 должен отвергаться")
	}
	if _, err := validateMapTarget("dominator", "bad host!", 502); err == nil {
		t.Fatal("недопустимые символы в хосте должны отвергаться")
	}
}
