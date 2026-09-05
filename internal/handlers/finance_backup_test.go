package handlers

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseBackupTime(t *testing.T) {
	want := time.Date(2026, 9, 3, 14, 30, 0, 0, time.UTC)

	cases := []string{
		"2026-09-03T14:30:00Z",
		"2026-09-03T14:30:00",
		"2026-09-03 14:30:00",
	}
	for _, in := range cases {
		got, err := parseBackupTime(in)
		if err != nil {
			t.Errorf("parseBackupTime(%q): %v", in, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("parseBackupTime(%q) = %v, ожидалось %v", in, got, want)
		}
	}

	if got, err := parseBackupTime("2026-09-03"); err != nil {
		t.Errorf("parseBackupTime(дата без времени): %v", err)
	} else if !got.Equal(time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("parseBackupTime(дата без времени) = %v", got)
	}

	for _, bad := range []string{"", "   ", "03.09.2026", "не дата"} {
		if _, err := parseBackupTime(bad); err == nil {
			t.Errorf("parseBackupTime(%q) должен вернуть ошибку", bad)
		}
	}
}

func TestNormalizeSplitValue(t *testing.T) {
	cases := []struct {
		num, denom     int64
		wantNum, wantD int64
	}{
		{12345, 100, 12345, 100},     // уже в копейках
		{-500, 100, -500, 100},       // отрицательные не ломаются
		{7, 100, 7, 100},             // небольшая сумма в сотых
		{1234560, 1000, 123456, 100}, // точное преобразование тысячных
		{5, 1, 500, 100},             // целые единицы
	}
	for _, c := range cases {
		gotNum, gotDenom, err := normalizeSplitValue(c.num, c.denom)
		if err != nil || gotNum != c.wantNum || gotDenom != c.wantD {
			t.Errorf("normalizeSplitValue(%d, %d) = (%d, %d), ожидалось (%d, %d)",
				c.num, c.denom, gotNum, gotDenom, c.wantNum, c.wantD)
		}
	}
}

func TestNormalizeBackupMode(t *testing.T) {
	if normalizeBackupMode("merge") != "merge" || normalizeBackupMode(" MERGE ") != "merge" {
		t.Error("merge должен распознаваться независимо от регистра и пробелов")
	}
	// Пустое значение сохраняет прежний режим по умолчанию.
	for _, in := range []string{"", "replace"} {
		if got := normalizeBackupMode(in); got != "replace" {
			t.Errorf("normalizeBackupMode(%q) = %q, ожидалось replace", in, got)
		}
	}
}

func TestReadBackupRequest_Multipart(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "backup.json")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fw.Write([]byte(`{"format":"finforme-backup"}`))
	mw.WriteField("mode", "merge")
	mw.Close()

	r := httptest.NewRequest("POST", "/api/v1/finance/import/json", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())

	body, mode, err := readBackupRequest(r)
	if err != nil {
		t.Fatalf("readBackupRequest: %v", err)
	}
	if string(body) != `{"format":"finforme-backup"}` {
		t.Errorf("неожиданное содержимое файла: %s", body)
	}
	if mode != "merge" {
		t.Errorf("mode = %q, ожидалось merge", mode)
	}
}

func TestReadBackupRequest_RawBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/v1/finance/import/json?mode=merge",
		strings.NewReader(`{"format":"finforme-backup"}`))
	r.Header.Set("Content-Type", "application/json")

	body, mode, err := readBackupRequest(r)
	if err != nil {
		t.Fatalf("readBackupRequest: %v", err)
	}
	if len(body) == 0 || mode != "merge" {
		t.Errorf("body=%q mode=%q", body, mode)
	}

	// Пустое тело — понятная ошибка, а не паника при разборе JSON
	empty := httptest.NewRequest("POST", "/api/v1/finance/import/json", strings.NewReader(""))
	empty.Header.Set("Content-Type", "application/json")
	if _, _, err := readBackupRequest(empty); err == nil {
		t.Error("пустое тело должно возвращать ошибку")
	}
}

// TestRestoreBackup_Validation проверяет проверки, которые срабатывают до обращения к БД,
// поэтому Handler можно создать без соединения.
func TestRestoreBackup_Validation(t *testing.T) {
	h := &Handler{}

	cases := []struct {
		name string
		data *backupData
	}{
		{
			name: "чужой формат",
			data: &backupData{Format: "gnucash", Accounts: []backupAccount{{ID: 1, Name: "A", AccountType: "ASSET"}}},
		},
		{
			name: "версия из будущего",
			data: &backupData{Format: backupFormat, Version: backupFormatVersion + 1,
				Accounts: []backupAccount{{ID: 1, Name: "A", AccountType: "ASSET"}}},
		},
		{
			name: "пустой файл",
			data: &backupData{Format: backupFormat, Version: backupFormatVersion},
		},
		{
			name: "счёт без id",
			data: &backupData{Format: backupFormat, Accounts: []backupAccount{{Name: "A", AccountType: "ASSET"}}},
		},
		{
			name: "дубль id счёта",
			data: &backupData{Format: backupFormat, Accounts: []backupAccount{
				{ID: 1, Name: "A", AccountType: "ASSET"},
				{ID: 1, Name: "B", AccountType: "ASSET"},
			}},
		},
		{
			name: "неизвестный тип счёта",
			data: &backupData{Format: backupFormat, Accounts: []backupAccount{{ID: 1, Name: "A", AccountType: "WALLET"}}},
		},
		{
			name: "пустое имя счёта",
			data: &backupData{Format: backupFormat, Accounts: []backupAccount{{ID: 1, Name: "  ", AccountType: "ASSET"}}},
		},
		{
			name: "ссылка на несуществующего родителя",
			data: &backupData{Format: backupFormat, Accounts: []backupAccount{
				{ID: 1, Name: "A", AccountType: "ASSET", ParentID: ptrInt64(42)},
			}},
		},
		{
			name: "сплит на несуществующий счёт",
			data: &backupData{Format: backupFormat,
				Accounts: []backupAccount{{ID: 1, Name: "A", AccountType: "ASSET"}},
				Transactions: []backupTransaction{{
					ID: 1, Description: "Покупка", PostDate: "2026-09-03T00:00:00Z",
					Splits: []backupSplit{{AccountID: 99, ValueNum: 100, ValueDenom: 100}},
				}},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := h.restoreBackup(1, c.data, "replace")
			if err == nil {
				t.Fatal("ожидалась ошибка валидации")
			}
			var vErr validationError
			if !errors.As(err, &vErr) {
				t.Fatalf("ожидалась validationError, получено: %v", err)
			}
		})
	}
}

func ptrInt64(v int64) *int64 { return &v }
