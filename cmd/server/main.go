package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"

	"github.com/evbogdanov/finforme/internal/config"
	"github.com/evbogdanov/finforme/internal/database"
	"github.com/evbogdanov/finforme/internal/handlers"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	// Загрузка конфигурации
	cfg := config.Load()

	// Подключение к базе данных
	db, err := sql.Open("sqlite3", cfg.DatabasePath)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	// Инициализация базы данных
	if err := database.InitDB(db); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	// Создание хранилища сессий
	store := sessions.NewCookieStore([]byte(cfg.SessionSecret))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 дней
		HttpOnly: true,
		Secure:   cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
	}

	// Создание обработчиков
	h := handlers.New(db, store)

	// Настройка роутера
	r := mux.NewRouter()

	// Статические файлы
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Главная страница
	r.HandleFunc("/", h.Index).Methods("GET")

	// Аутентификация
	r.HandleFunc("/accounts/login/", h.Login).Methods("GET", "POST")
	r.HandleFunc("/accounts/logout/", h.Logout).Methods("GET")
	r.HandleFunc("/accounts/register/", h.Register).Methods("GET", "POST")
	r.HandleFunc("/accounts/info/", h.RequireAuth(h.AccountInfo)).Methods("GET")
	r.HandleFunc("/accounts/password_change/", h.RequireAuth(h.PasswordChange)).Methods("GET", "POST")
	r.HandleFunc("/accounts/change_info/", h.RequireAuth(h.ChangeInfo)).Methods("GET", "POST")

	// Финансы
	r.HandleFunc("/finance/", h.RequireAuth(h.FinanceIndex)).Methods("GET")
	r.HandleFunc("/finance/account/new", h.RequireAuth(h.FinanceAccountEdit)).Methods("GET")
	r.HandleFunc("/finance/account/{id}/edit", h.RequireAuth(h.FinanceAccountEdit)).Methods("GET")
	r.HandleFunc("/finance/account/{id}", h.RequireAuth(h.FinanceAccountView)).Methods("GET")
	r.HandleFunc("/finance/transaction/{account_id}/", h.RequireAuth(h.FinanceTransaction)).Methods("GET")
	r.HandleFunc("/finance/transaction/{account_id}/{tx_id}", h.RequireAuth(h.FinanceTransaction)).Methods("GET")
	r.HandleFunc("/finance/tag/{tag}", h.RequireAuth(h.FinanceTransactionsByTag)).Methods("GET")
	r.HandleFunc("/finance/settings", h.RequireAuth(h.FinanceSettings)).Methods("GET")

	// API
	api := r.PathPrefix("/api/v1").Subrouter()
	api.Use(h.RequireAuthMiddleware)
	api.HandleFunc("/finance/accounts/get", h.APIAccountsGet).Methods("GET")
	api.HandleFunc("/finance/account/save", h.APIAccountSave).Methods("POST")
	api.HandleFunc("/finance/account/delete", h.APIAccountDelete).Methods("DELETE")
	api.HandleFunc("/finance/transactions/get", h.APITransactionsGet).Methods("GET")
	api.HandleFunc("/finance/transaction/save", h.APITransactionSave).Methods("POST")
	api.HandleFunc("/finance/transaction/delete", h.APITransactionDelete).Methods("DELETE")
	api.HandleFunc("/finance/export/json", h.APIExportJSON).Methods("GET")
	api.HandleFunc("/finance/delete", h.APIDataDelete).Methods("DELETE")
	api.HandleFunc("/finance/welcome/createempty", h.APIWelcomeCreateEmpty).Methods("POST")
	api.HandleFunc("/finance/welcome/createbase", h.APIWelcomeCreateBase).Methods("POST")
	api.HandleFunc("/finance/welcome/importjson", h.APIImportJSON).Methods("POST")
	api.HandleFunc("/finance/welcome/import", h.APIImportGnuCash).Methods("POST")

	// Запуск сервера
	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Starting server on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal("Server failed:", err)
	}
}
