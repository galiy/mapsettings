// map-settings — транспортный слой: чтение/запись ячеек МАП.
// Copyright (C) 2026  Aleksandr Galinskii
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cellWrite — запись одного байта в ячейку.
type cellWrite struct {
	Addr  uint16
	Value byte
}

// cellReader — чтение байтовых ячеек.
type cellReader interface {
	ReadCells(ctx context.Context, addrs []uint16) (map[uint16]byte, []string)
}

// cellWriter — запись байтовых ячеек (транспорт сам делает служебное обрамление
// команд там, где это нужно).
type cellWriter interface {
	WriteBytes(ctx context.Context, writes []cellWrite) error
}

// cellConn — соединение для чтения, записи ячеек и команд.
type cellConn interface {
	cellReader
	cellWriter
	WriteCommand(ctx context.Context, cmd byte) error
	io.Closer
}

// connTimeout — общий таймаут операций транспорта.
const connTimeout = 90 * time.Second

// openConn открывает соединение по конфигу.
func openConn(c connConfig) (cellConn, error) {
	switch c.Protocol {
	case "modbus":
		if c.Transport == "com" {
			return openModbusRTU(c)
		}
		return openModbusTCP(c)
	case "microart":
		return openMicroArt(c)
	case "malina":
		return openMalina(c)
	}
	return nil, fmt.Errorf("неизвестный протокол %q", c.Protocol)
}

// validateConnConfig проверяет параметры связи.
func validateConnConfig(c connConfig, forWrite bool) error {
	switch c.Protocol {
	case "modbus":
		if forWrite {
			return fmt.Errorf("запись по Modbus не поддерживается (выберите MICROART или Малину)")
		}
		return validateTcpCom(c)
	case "microart":
		return validateTcpCom(c)
	case "malina":
		if strings.TrimSpace(c.Host) == "" {
			return fmt.Errorf("не задан адрес Малины")
		}
		if c.Login == "" {
			return fmt.Errorf("не задан логин Малины")
		}
		return nil
	}
	return fmt.Errorf("неизвестный протокол %q", c.Protocol)
}

func validateTcpCom(c connConfig) error {
	switch c.Transport {
	case "com":
		if strings.TrimSpace(c.Serial) == "" {
			return fmt.Errorf("не задан COM-порт")
		}
		if c.Baud <= 0 {
			return fmt.Errorf("не задана скорость COM")
		}
		return nil
	case "tcp", "":
		if strings.TrimSpace(c.IP) == "" {
			return fmt.Errorf("не задан IP")
		}
		return nil
	}
	return fmt.Errorf("неизвестный транспорт %q", c.Transport)
}

// ---------------------------------------------------------------- Modbus TCP

type modbusTCPConn struct{ c *Client }

func openModbusTCP(c connConfig) (cellConn, error) {
	if err := validateTcpCom(c); err != nil {
		return nil, err
	}
	unit := byte(c.Unit)
	if unit == 0 {
		unit = 1
	}
	port := c.Port
	if port == 0 {
		port = 502
	}
	return &modbusTCPConn{c: &Client{Address: net.JoinHostPort(c.IP, strconv.Itoa(port)), Unit: unit}}, nil
}

func (m *modbusTCPConn) ReadCells(ctx context.Context, addrs []uint16) (map[uint16]byte, []string) {
	return readCellRanges(ctx, m.c, addrs)
}

func (m *modbusTCPConn) WriteBytes(ctx context.Context, writes []cellWrite) error {
	if len(writes) == 0 {
		return nil
	}
	if err := m.c.WriteCommand(ctx, ComMAPEEPromWR); err != nil {
		return fmt.Errorf("разрешение записи: %w", err)
	}
	for _, w := range writes {
		if err := m.c.WriteCell(ctx, w.Addr, w.Value); err != nil {
			return fmt.Errorf("запись 0x%03X: %w", w.Addr, err)
		}
	}
	return m.c.WriteCommand(ctx, ComMAPCallLoadEEProm)
}

func (m *modbusTCPConn) WriteCommand(ctx context.Context, cmd byte) error {
	return m.c.WriteCommand(ctx, cmd)
}

func (m *modbusTCPConn) Close() error { m.c.Close(); return nil }

// ------------------------------------------------------------- Modbus RTU (COM)

type modbusRTUConn struct {
	port io.ReadWriteCloser
	unit byte
	mu   sync.Mutex
}

func openModbusRTU(c connConfig) (cellConn, error) {
	if err := validateTcpCom(c); err != nil {
		return nil, err
	}
	p, err := openSerial(c.Serial, c.Baud)
	if err != nil {
		return nil, err
	}
	unit := byte(c.Unit)
	if unit == 0 {
		unit = 1
	}
	return &modbusRTUConn{port: p, unit: unit}, nil
}

func (m *modbusRTUConn) transact(ctx context.Context, pdu []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	req := append([]byte{m.unit}, pdu...)
	crc := crc16(req)
	req = append(req, byte(crc&0xff), byte(crc>>8))
	if err := setDeadline(m.port, time.Now().Add(ReadTimeout)); err != nil {
		return nil, err
	}
	if _, err := m.port.Write(req); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	// Ответ: unit+func+...+(crc). Читаем минимум 5 байт, длину уточняем по байтам.
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(m.port, hdr); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	var rest []byte
	if hdr[1]&0x80 != 0 {
		rest = make([]byte, 3) // exc + crc2
	} else {
		switch hdr[1] {
		case 0x03:
			bc := make([]byte, 1)
			if _, err := io.ReadFull(m.port, bc); err != nil {
				return nil, err
			}
			rest = append(rest, bc[0])
			body := make([]byte, int(bc[0])+2)
			if _, err := io.ReadFull(m.port, body); err != nil {
				return nil, err
			}
			rest = append(rest, body...)
		default:
			rest = make([]byte, 6) // 0x06 эхо: addr2+val2+crc2
		}
	}
	if hdr[1] != 0x03 {
		if _, err := io.ReadFull(m.port, rest); err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
	}
	full := append(append([]byte{}, hdr...), rest...)
	gotCRC := uint16(full[len(full)-2]) | uint16(full[len(full)-1])<<8
	if crc16(full[:len(full)-2]) != gotCRC {
		return nil, fmt.Errorf("bad crc")
	}
	if hdr[1]&0x80 != 0 {
		return nil, fmt.Errorf("modbus exception func=0x%02X code=0x%02X", hdr[1], rest[0])
	}
	return full[1 : len(full)-2], nil
}

func (m *modbusRTUConn) ReadCells(ctx context.Context, addrs []uint16) (map[uint16]byte, []string) {
	out := map[uint16]byte{}
	var errs []string
	for _, sp := range addrSpans(addrs, 120) {
		count := uint16(sp[1] - sp[0] + 1)
		pdu := []byte{0x03, byte(sp[0] >> 8), byte(sp[0]), byte(count >> 8), byte(count)}
		rest, err := m.transact(ctx, pdu)
		if err != nil {
			errs = append(errs, fmt.Sprintf("чтение 0x%03X..0x%03X: %v", sp[0], sp[1], err))
			continue
		}
		if len(rest) < 2 || rest[0] != 0x03 {
			errs = append(errs, fmt.Sprintf("чтение 0x%03X: неожиданный ответ", sp[0]))
			continue
		}
		data := rest[2:]
		for i := 0; i < len(data); i++ {
			out[uint16(int(sp[0])+i)] = data[i]
		}
	}
	return out, errs
}

func (m *modbusRTUConn) WriteBytes(ctx context.Context, writes []cellWrite) error {
	if len(writes) == 0 {
		return nil
	}
	if _, err := m.transact(ctx, []byte{0x06, 0x00, 0x00, 0x00, ComMAPEEPromWR}); err != nil {
		return fmt.Errorf("разрешение записи: %w", err)
	}
	for _, w := range writes {
		if _, err := m.transact(ctx, []byte{0x06, byte(w.Addr >> 8), byte(w.Addr), 0x00, w.Value}); err != nil {
			return fmt.Errorf("запись 0x%03X: %w", w.Addr, err)
		}
	}
	if _, err := m.transact(ctx, []byte{0x06, 0x00, 0x00, 0x00, ComMAPCallLoadEEProm}); err != nil {
		return fmt.Errorf("фиксация: %w", err)
	}
	return nil
}

func (m *modbusRTUConn) WriteCommand(ctx context.Context, cmd byte) error {
	_, err := m.transact(ctx, []byte{0x06, 0x00, 0x00, 0x00, cmd})
	return err
}

func (m *modbusRTUConn) Close() error { return m.port.Close() }

// ---------------------------------------------------------------- Malina (HTTP)

type malinaConn struct {
	base  string
	login string
	pass  string
	hc    *http.Client
}

func openMalina(c connConfig) (cellConn, error) {
	if err := validateConnConfig(c, false); err != nil {
		return nil, err
	}
	host := strings.TrimSpace(c.Host)
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return &malinaConn{
		base:  strings.TrimRight(host, "/"),
		login: c.Login,
		pass:  c.Password,
		hc:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (m *malinaConn) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.base+path, nil)
	if err != nil {
		return nil, err
	}
	if m.login != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(m.login+":"+m.pass)))
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return b, nil
}

func (m *malinaConn) post(ctx context.Context, path, form string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.base+path, strings.NewReader(form))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if m.login != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(m.login+":"+m.pass)))
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return b, nil
}

func (m *malinaConn) ReadCells(ctx context.Context, addrs []uint16) (map[uint16]byte, []string) {
	out := map[uint16]byte{}
	var errs []string
	for _, sp := range addrSpans(addrs, 2048) {
		offset := int(sp[0])
		count := sp[1] - sp[0] + 1
		b, err := m.get(ctx, fmt.Sprintf("/read_memory.php?offset=%d&count=%d", offset, count))
		if err != nil {
			errs = append(errs, fmt.Sprintf("read_memory %d..%d: %v", sp[0], sp[1], err))
			continue
		}
		// read_memory.php отдаёт объект {"259":2,...}; при offset=0 с
		// последовательными ключами PHP кодирует его как JSON-массив.
		var raw any
		if err := json.Unmarshal(b, &raw); err != nil {
			errs = append(errs, fmt.Sprintf("read_memory %d: разбор json: %v", offset, err))
			continue
		}
		switch v := raw.(type) {
		case map[string]any:
			for k, val := range v {
				a, err := strconv.Atoi(k)
				if err != nil {
					continue
				}
				if n, ok := toInt(val); ok {
					out[uint16(a)] = byte(n)
				}
			}
		case []any:
			for i, val := range v {
				if n, ok := toInt(val); ok {
					out[uint16(offset+i)] = byte(n)
				}
			}
		default:
			errs = append(errs, fmt.Sprintf("read_memory %d: неожиданный формат ответа", offset))
		}
	}
	return out, errs
}

func (m *malinaConn) WriteBytes(ctx context.Context, writes []cellWrite) error {
	if len(writes) == 0 {
		return nil
	}
	pairs := make([][2]int, 0, len(writes))
	for _, w := range writes {
		pairs = append(pairs, [2]int{int(w.Addr), int(w.Value)})
	}
	body, _ := json.Marshal(pairs)
	form := "data=" + url.QueryEscape(string(body))
	resp, err := m.post(ctx, "/write_eeprom.php", form)
	if err != nil {
		return err
	}
	txt := strings.TrimSpace(string(resp))
	if txt != "Ok" {
		return fmt.Errorf("write_eeprom: %s", txt)
	}
	return nil
}

func (m *malinaConn) WriteCommand(ctx context.Context, cmd byte) error {
	return m.WriteBytes(ctx, []cellWrite{{0x0000, cmd}})
}

func (m *malinaConn) Close() error { return nil }

// ------------------------------------------------------------------ MicroArt

// microartConn — «родной» ASCII-протокол МАП (посылки r/w/o/e).
type microartConn struct {
	conn io.ReadWriteCloser
	echo bool // COM: эхо на каждый байт
	mu   sync.Mutex
}

func openMicroArt(c connConfig) (cellConn, error) {
	if err := validateTcpCom(c); err != nil {
		return nil, err
	}
	if c.Transport == "com" {
		p, err := openSerial(c.Serial, c.Baud)
		if err != nil {
			return nil, err
		}
		return &microartConn{conn: p, echo: true}, nil
	}
	port := c.Port
	if port == 0 {
		port = 502
	}
	d := net.Dialer{Timeout: ConnectTimeout}
	nc, err := d.Dial("tcp", net.JoinHostPort(c.IP, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", c.IP, err)
	}
	return &microartConn{conn: nc, echo: false}, nil
}

func (m *microartConn) WriteCommand(ctx context.Context, cmd byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd(0, cmd)
}

func (m *microartConn) Close() error { return m.conn.Close() }

// microartFrame собирает посылку: экранирование, контрольная сумма, '\n'.
func microartFrame(payload []byte) []byte {
	out := make([]byte, 0, len(payload)+4)
	sum := 0
	for _, b := range payload {
		switch b {
		case 0x0A:
			out = append(out, 0xDB, 0xDC)
			sum += 0xDB + 0xDC
		case 0xDB:
			out = append(out, 0xDB, 0xDD)
			sum += 0xDB + 0xDD
		default:
			out = append(out, b)
			sum += int(b)
		}
	}
	s := byte((0x100 - (sum & 0xff)) & 0xff)
	out = append(out, s)
	if s != 0x0A {
		out = append(out, '\n')
	}
	return out
}

// writeFrame отправляет посылку (с эхо для COM).
func (m *microartConn) writeFrame(frame []byte) error {
	if !m.echo {
		_, err := m.conn.Write(frame)
		return err
	}
	for i := 0; i < len(frame); {
		if _, err := m.conn.Write(frame[i : i+1]); err != nil {
			return err
		}
		echo := make([]byte, 1)
		if _, err := io.ReadFull(m.conn, echo); err != nil {
			return fmt.Errorf("эхо: %w", err)
		}
		if echo[0] != frame[i] {
			return fmt.Errorf("несовпадение эхо: отправили 0x%02X, получили 0x%02X", frame[i], echo[0])
		}
		i++
	}
	return nil
}

// readFrame читает ответ до '\n', декодирует экранирование и проверяет сумму.
func (m *microartConn) readFrame() ([]byte, error) {
	var raw []byte
	buf := make([]byte, 1)
	for {
		if _, err := io.ReadFull(m.conn, buf); err != nil {
			return nil, err
		}
		b := buf[0]
		if m.echo {
			_, _ = m.conn.Write([]byte{b})
		}
		if b == 0x0A {
			break
		}
		raw = append(raw, b)
		if len(raw) > 4096 {
			return nil, fmt.Errorf("слишком длинный ответ")
		}
	}
	if len(raw) < 1 {
		return nil, fmt.Errorf("пустой ответ")
	}
	// Сумма считается по сырым (экранированным) байтам без последней S.
	body, s := raw[:len(raw)-1], raw[len(raw)-1]
	sum := 0
	for _, x := range body {
		sum += int(x)
	}
	if byte((0x100-(sum&0xff))&0xff) != s {
		return nil, fmt.Errorf("неверная контрольная сумма")
	}
	// Декодирование экранирования.
	dec := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		if body[i] == 0xDB && i+1 < len(body) {
			switch body[i+1] {
			case 0xDC:
				dec = append(dec, 0x0A)
			case 0xDD:
				dec = append(dec, 0xDB)
			default:
				dec = append(dec, 0xDB)
			}
			i++
			continue
		}
		dec = append(dec, body[i])
	}
	return dec, nil
}

func (m *microartConn) ReadCells(ctx context.Context, addrs []uint16) (map[uint16]byte, []string) {
	out := map[uint16]byte{}
	var errs []string
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sp := range addrSpans(addrs, 256) {
		count := sp[1] - sp[0] + 1
		payload := []byte{'r', byte(count - 1), byte(sp[0] >> 8), byte(sp[0])}
		if err := setDeadline(m.conn, time.Now().Add(ReadTimeout)); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if err := m.writeFrame(microartFrame(payload)); err != nil {
			errs = append(errs, fmt.Sprintf("чтение 0x%03X: %v", sp[0], err))
			continue
		}
		dec, err := m.readFrame()
		if err != nil {
			errs = append(errs, fmt.Sprintf("чтение 0x%03X: %v", sp[0], err))
			continue
		}
		if len(dec) < 1 {
			errs = append(errs, fmt.Sprintf("чтение 0x%03X: пусто", sp[0]))
			continue
		}
		switch dec[0] {
		case 'o':
			data := dec[1:]
			for i := 0; i < len(data) && i < int(count); i++ {
				out[uint16(int(sp[0])+i)] = data[i]
			}
		case 'e':
			code := byte(0)
			if len(dec) > 1 {
				code = dec[1]
			}
			errs = append(errs, fmt.Sprintf("чтение 0x%03X: ошибка МАП 0x%02X", sp[0], code))
		default:
			errs = append(errs, fmt.Sprintf("чтение 0x%03X: неожиданный ответ", sp[0]))
		}
	}
	return out, errs
}

func (m *microartConn) WriteBytes(ctx context.Context, writes []cellWrite) error {
	if len(writes) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// 1) разрешение записи: w 0 0000 03
	if err := m.cmd(0, ComMAPEEPromWR); err != nil {
		return fmt.Errorf("разрешение записи: %w", err)
	}
	// 2) запись страницами (непрерывные группы)
	if err := m.writeGroups(writes); err != nil {
		return fmt.Errorf("запись: %w", err)
	}
	// 3) фиксация EEPROM: w 0 0000 07
	if err := m.cmd(0, ComMAPCallLoadEEProm); err != nil {
		return fmt.Errorf("фиксация: %w", err)
	}
	return nil
}

func (m *microartConn) cmd(addr uint16, value byte) error {
	payload := []byte{'w', 0x00, byte(addr >> 8), byte(addr), value}
	if err := setDeadline(m.conn, time.Now().Add(ReadTimeout)); err != nil {
		return err
	}
	if err := m.writeFrame(microartFrame(payload)); err != nil {
		return err
	}
	dec, err := m.readFrame()
	if err != nil {
		return err
	}
	if len(dec) == 0 || dec[0] != 'o' {
		if len(dec) > 1 {
			return fmt.Errorf("ошибка МАП 0x%02X", dec[1])
		}
		return fmt.Errorf("неожиданный ответ")
	}
	return nil
}

func (m *microartConn) writeGroups(writes []cellWrite) error {
	sort.Slice(writes, func(i, j int) bool { return writes[i].Addr < writes[j].Addr })
	for i := 0; i < len(writes); {
		j := i
		for j+1 < len(writes) && writes[j+1].Addr == writes[j].Addr+1 && (j-i+1) < 256 {
			j++
		}
		group := writes[i : j+1]
		payload := []byte{'w', byte(len(group) - 1), byte(group[0].Addr >> 8), byte(group[0].Addr)}
		for _, w := range group {
			payload = append(payload, w.Value)
		}
		if err := m.cmdRaw(payload); err != nil {
			return err
		}
		i = j + 1
	}
	return nil
}

func (m *microartConn) cmdRaw(payload []byte) error {
	if err := setDeadline(m.conn, time.Now().Add(ReadTimeout)); err != nil {
		return err
	}
	if err := m.writeFrame(microartFrame(payload)); err != nil {
		return err
	}
	_, err := m.readFrame()
	return err
}

// addrSpans группирует отсортированные адреса в диапазоны не длиннее maxCount.
func addrSpans(addrs []uint16, maxCount int) [][2]uint16 {
	if len(addrs) == 0 {
		return nil
	}
	uniq := append([]uint16(nil), addrs...)
	sort.Slice(uniq, func(i, j int) bool { return uniq[i] < uniq[j] })
	var out [][2]uint16
	start, prev := uniq[0], uniq[0]
	flush := func() {
		out = append(out, [2]uint16{start, prev})
	}
	for _, a := range uniq[1:] {
		if a == prev || a == prev+1 {
			if int(a-start)+1 > maxCount {
				flush()
				start, prev = a, a
				continue
			}
			prev = a
			continue
		}
		flush()
		start, prev = a, a
	}
	flush()
	return out
}

// crc16 — CRC-16/Modbus (полином 0xA001, начальное 0xFFFF).
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

type deadlineSetter interface{ SetDeadline(time.Time) error }

// setDeadline выставляет таймаут, если соединение это поддерживает.
func setDeadline(c io.ReadWriteCloser, t time.Time) error {
	if d, ok := c.(deadlineSetter); ok {
		return d.SetDeadline(t)
	}
	return nil
}

// toInt приводит значение из JSON к int (число или строка).
func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case string:
		n, err := strconv.Atoi(x)
		return n, err == nil
	}
	return 0, false
}
