package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// MemorySettings exposes the same per-user store as the MCP tools.
func (h *Handler) MemorySettings(w http.ResponseWriter, r *http.Request) {
	userID, _ := h.getUserID(r)
	var form memoryEntry
	editing := false
	status := http.StatusOK
	message := ""
	if r.Method == http.MethodPost {
		// 16000 Unicode characters can occupy up to 192000 URL-encoded bytes.
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Не удалось прочитать форму", http.StatusBadRequest)
			return
		}
		form.Key = r.PostForm.Get("key")
		form.Content = r.PostForm.Get("content")
		editing = r.PostForm.Get("editing") == "1"
		var err error
		action := r.PostForm.Get("action")
		switch action {
		case "save":
			_, err = h.saveMemory(r.Context(), userID, saveMemoryIn{Key: form.Key, Content: form.Content})
		case "delete":
			_, err = h.deleteMemory(r.Context(), userID, form.Key)
		default:
			err = validationError("Неизвестное действие")
		}
		if err == nil {
			msg := "saved"
			if action == "delete" {
				msg = "deleted"
			}
			http.Redirect(w, r, "/finance/settings/memory?msg="+msg, http.StatusSeeOther)
			return
		}
		var invalid validationError
		if !errors.As(err, &invalid) {
			http.Error(w, "Не удалось сохранить изменения", http.StatusInternalServerError)
			return
		}
		message = err.Error()
		status = http.StatusBadRequest
	} else if r.URL.Query().Has("edit") {
		key := r.URL.Query().Get("edit")
		if err := validateMemoryKey(key); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var err error
		form, err = h.getMemory(r.Context(), userID, key)
		if err != nil {
			var invalid validationError
			if errors.As(err, &invalid) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "Не удалось загрузить заметку", http.StatusInternalServerError)
			}
			return
		}
		editing = true
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > int(^uint(0)>>1)-50 {
			http.Error(w, "Некорректная страница", http.StatusBadRequest)
			return
		}
		offset = parsed
	}
	prefix := r.URL.Query().Get("prefix")
	memories, err := h.listMemory(r.Context(), userID, listMemoryIn{Prefix: prefix, Limit: 50, Offset: offset})
	if err != nil {
		var invalid validationError
		if errors.As(err, &invalid) {
			http.Error(w, err.Error(), http.StatusBadRequest)
		} else {
			http.Error(w, "Не удалось загрузить память", http.StatusInternalServerError)
		}
		return
	}
	data := h.pageData(userID, "settings")
	data["Title"] = "Память ассистента"
	data["MemoryForm"] = form
	data["Editing"] = editing
	data["Memories"] = memories.Memories
	data["HasMore"] = memories.HasMore
	data["Offset"] = offset
	data["NextOffset"] = offset + 50
	previous := offset - 50
	if previous < 0 {
		previous = 0
	}
	data["PreviousOffset"] = previous
	data["Prefix"] = prefix
	data["Error"] = message
	data["Message"] = r.URL.Query().Get("msg")
	var body strings.Builder
	if err := h.templates.ExecuteTemplate(&body, "memory_settings.html", data); err != nil {
		http.Error(w, "Не удалось загрузить страницу", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write([]byte(body.String()))
}
