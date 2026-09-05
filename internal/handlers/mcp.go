package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpUserKey — ключ контекста для передачи userID из auth-middleware в getServer.
type mcpUserKey struct{}

// MCPHandler возвращает HTTP-обработчик MCP-сервера (Streamable HTTP, stateless).
// Авторизация — через Authorization: Bearer <api-token>; сервер строится
// под конкретного пользователя.
func (h *Handler) MCPHandler() http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		userID, ok := r.Context().Value(mcpUserKey{}).(int64)
		if !ok {
			return nil
		}
		return h.buildMCPServer(userID)
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})

	// Bearer-авторизация перед передачей запроса в MCP-обработчик
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := h.userIDFromBearer(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="finforme", error="invalid_token"`)
			http.Error(w, "Unauthorized: valid API token required", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), mcpUserKey{}, userID)
		streamable.ServeHTTP(w, r.WithContext(ctx))
	})
}

// buildMCPServer собирает MCP-сервер с инструментами, привязанными к userID.
func (h *Handler) buildMCPServer(userID int64) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "finforme",
		Version: "1.0.0",
	}, &mcp.ServerOptions{
		Instructions: "Доступ к личным финансам Finforme: счета, транзакции (двойная " +
			"запись), отчёты по доходам/расходам и курсы валют. Суммы — в валюте счёта. " +
			"Даты — в формате YYYY-MM-DD.",
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_accounts",
		Description: "Список счетов пользователя: balance — собственный баланс в currency, balances — суммы вместе с дочерними счетами отдельно по валютам (включая скрытые). " +
			"Контейнерные (placeholder) счета не участвуют в транзакциях.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listAccountsIn) (*mcp.CallToolResult, listAccountsOut, error) {
		accounts, err := h.accountsWithBalances(userID)
		if err != nil {
			return nil, listAccountsOut{}, err
		}
		if !in.IncludeHidden {
			filtered := accounts[:0]
			for _, a := range accounts {
				if !a.Hidden {
					filtered = append(filtered, a)
				}
			}
			accounts = filtered
		}
		return nil, listAccountsOut{Accounts: accounts}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_transactions",
		Description: "Список транзакций с фильтрами: счёт, диапазон дат, тег, поиск по описанию. " +
			"Каждая транзакция содержит сплиты (двойная запись). Сортировка: новые сверху.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listTransactionsIn) (*mcp.CallToolResult, listTransactionsOut, error) {
		f := txListFilter{
			AccountID: in.AccountID,
			Tag:       in.Tag,
			Search:    in.Search,
			Limit:     in.Limit,
			Offset:    in.Offset,
		}
		var err error
		if f.From, err = parseOptionalDate(in.From); err != nil {
			return nil, listTransactionsOut{}, fmt.Errorf("invalid 'from': %w", err)
		}
		if f.To, err = parseOptionalDate(in.To); err != nil {
			return nil, listTransactionsOut{}, fmt.Errorf("invalid 'to': %w", err)
		}
		txs, err := h.listTransactions(userID, f)
		if err != nil {
			return nil, listTransactionsOut{}, err
		}
		return nil, listTransactionsOut{Transactions: txs}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_transaction",
		Description: "Получить одну транзакцию по ID со всеми сплитами (дебет/кредит).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getTransactionIn) (*mcp.CallToolResult, getTransactionOut, error) {
		tx, debit, credit := h.getTransaction(userID, in.ID)
		if tx == nil {
			return nil, getTransactionOut{}, fmt.Errorf("транзакция %d не найдена", in.ID)
		}
		return nil, getTransactionOut{
			ID:          tx.ID,
			Date:        tx.PostDate.Format("2006-01-02"),
			Description: tx.Description,
			Tags:        tx.Tags,
			Debit:       debit,
			Credit:      credit,
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "create_transaction",
		Description: "Создать транзакцию (двойная запись). Деньги списываются со счёта " +
			"from_account_id и зачисляются на to_account_id. Пример: расход — from=счёт актива, " +
			"to=счёт расхода. amount — в валюте from; для кросс-валютных задайте amount_to в валюте to.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in writeTransactionIn) (*mcp.CallToolResult, writeTransactionOut, error) {
		return h.mcpSaveTransaction(userID, 0, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_transaction",
		Description: "Обновить транзакцию с двумя проводками. Для сложных операций используйте update_transaction_metadata: распределение сумм менять нельзя.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateTransactionIn) (*mcp.CallToolResult, writeTransactionOut, error) {
		if in.ID <= 0 {
			return nil, writeTransactionOut{}, fmt.Errorf("укажите ID существующей транзакции")
		}
		return h.mcpSaveTransaction(userID, in.ID, writeTransactionIn{
			Date:          in.Date,
			Description:   in.Description,
			Amount:        in.Amount,
			FromAccountID: in.FromAccountID,
			ToAccountID:   in.ToAccountID,
			AmountTo:      in.AmountTo,
			Tags:          in.Tags,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_transaction_metadata",
		Description: "Изменить только дату, описание и теги существующей операции, сохранив все проводки, счета и суммы. Подходит для сложных импортированных операций.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateTransactionMetadataIn) (*mcp.CallToolResult, writeTransactionOut, error) {
		date, err := time.Parse("2006-01-02", in.Date)
		if err != nil {
			return nil, writeTransactionOut{}, fmt.Errorf("invalid date: %w", err)
		}
		if err := h.updateTransactionMetadata(userID, in.ID, date, in.Description, in.Tags); err != nil {
			return nil, writeTransactionOut{}, err
		}
		return nil, writeTransactionOut{ID: in.ID, Result: "ok"}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_transaction",
		Description: "Удалить транзакцию по ID вместе со всеми её сплитами. Необратимо.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getTransactionIn) (*mcp.CallToolResult, deleteTransactionOut, error) {
		if err := h.deleteTransaction(userID, in.ID); err != nil {
			return nil, deleteTransactionOut{}, err
		}
		return nil, deleteTransactionOut{Deleted: true, ID: in.ID}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_report",
		Description: "Отчёт по доходам и расходам за период (from..to включительно), " +
			"разбитый по категориям и валютам. Итоги и чистый результат находятся в массиве totals отдельно для каждой валюты; конвертации нет.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getReportIn) (*mcp.CallToolResult, *PeriodReport, error) {
		from, err := time.Parse("2006-01-02", in.From)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid 'from', expected YYYY-MM-DD: %w", err)
		}
		to, err := time.Parse("2006-01-02", in.To)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid 'to', expected YYYY-MM-DD: %w", err)
		}
		report, err := h.periodReport(userID, from, to)
		if err != nil {
			return nil, nil, err
		}
		return nil, report, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_currency_rates",
		Description: "Актуальные курсы валют (USD/RUB, EUR/RUB и др.) с дневным изменением. Данные ЦБ РФ и бирж.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, currencyRatesOut, error) {
		rates, updatedAt, err := loadCurrencyRates(h.db)
		if err != nil {
			return nil, currencyRatesOut{}, err
		}
		out := currencyRatesOut{UpdatedAt: updatedAt, Rates: make([]currencyRateOut, 0, len(rates))}
		for _, r := range rates {
			out.Rates = append(out.Rates, currencyRateOut{
				Code:     r.Code,
				Name:     r.Name,
				Rate:     r.Rate,
				Change:   r.Change,
				Source:   r.Source,
				RateDate: r.RateDate,
			})
		}
		return nil, out, nil
	})

	return server
}

// mcpSaveTransaction — общий код create/update для MCP.
func (h *Handler) mcpSaveTransaction(userID, txID int64, in writeTransactionIn) (*mcp.CallToolResult, writeTransactionOut, error) {
	postDate, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		return nil, writeTransactionOut{}, fmt.Errorf("invalid 'date', expected YYYY-MM-DD: %w", err)
	}
	valueTarget := in.AmountTo
	savedID, err := h.saveTransaction(userID, txSaveInput{
		TxID:            txID,
		Description:     in.Description,
		PostDate:        postDate,
		Tags:            in.Tags,
		Value:           in.Amount,
		ValueTarget:     valueTarget,
		DebitAccountID:  in.ToAccountID,
		CreditAccountID: in.FromAccountID,
	})
	if err != nil {
		return nil, writeTransactionOut{}, err
	}
	return nil, writeTransactionOut{ID: savedID, Result: "ok"}, nil
}

// parseOptionalDate парсит дату YYYY-MM-DD; пустая строка → нулевое время.
func parseOptionalDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", s)
}

// --- Типы входа/выхода MCP-инструментов ---

type listAccountsIn struct {
	IncludeHidden bool `json:"include_hidden,omitempty" jsonschema:"включать ли скрытые счета (по умолчанию нет)"`
}

type listAccountsOut struct {
	Accounts []AccountBalance `json:"accounts"`
}

type listTransactionsIn struct {
	AccountID int64  `json:"account_id,omitempty" jsonschema:"фильтр по ID счёта; 0 или пусто — все счета"`
	From      string `json:"from,omitempty" jsonschema:"начало периода YYYY-MM-DD, включительно"`
	To        string `json:"to,omitempty" jsonschema:"конец периода YYYY-MM-DD, включительно"`
	Tag       string `json:"tag,omitempty" jsonschema:"фильтр по тегу (подстрока)"`
	Search    string `json:"search,omitempty" jsonschema:"поиск по описанию (подстрока)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"максимум записей (по умолчанию 50, максимум 500)"`
	Offset    int    `json:"offset,omitempty" jsonschema:"смещение для пагинации"`
}

type listTransactionsOut struct {
	Transactions []map[string]interface{} `json:"transactions"`
}

type getTransactionIn struct {
	ID int64 `json:"id" jsonschema:"ID транзакции"`
}

type getTransactionOut struct {
	ID          int64                    `json:"id"`
	Date        string                   `json:"date"`
	Description string                   `json:"description"`
	Tags        string                   `json:"tags"`
	Debit       []map[string]interface{} `json:"debit"`
	Credit      []map[string]interface{} `json:"credit"`
}

type writeTransactionIn struct {
	Date          string   `json:"date" jsonschema:"дата транзакции YYYY-MM-DD"`
	Description   string   `json:"description" jsonschema:"описание транзакции"`
	Amount        float64  `json:"amount" jsonschema:"сумма в валюте счёта списания (from), положительное число"`
	FromAccountID int64    `json:"from_account_id" jsonschema:"ID счёта списания (откуда уходят деньги)"`
	ToAccountID   int64    `json:"to_account_id" jsonschema:"ID счёта зачисления (куда приходят деньги)"`
	AmountTo      *float64 `json:"amount_to,omitempty" jsonschema:"сумма в валюте счёта зачисления обязательна для разных валют; для одной валюты по умолчанию равна amount"`
	Tags          string   `json:"tags,omitempty" jsonschema:"теги через запятую"`
}

type updateTransactionIn struct {
	ID            int64    `json:"id" jsonschema:"ID обновляемой транзакции"`
	Date          string   `json:"date" jsonschema:"дата транзакции YYYY-MM-DD"`
	Description   string   `json:"description" jsonschema:"описание транзакции"`
	Amount        float64  `json:"amount" jsonschema:"сумма в валюте счёта списания (from), положительное число"`
	FromAccountID int64    `json:"from_account_id" jsonschema:"ID счёта списания (откуда уходят деньги)"`
	ToAccountID   int64    `json:"to_account_id" jsonschema:"ID счёта зачисления (куда приходят деньги)"`
	AmountTo      *float64 `json:"amount_to,omitempty" jsonschema:"сумма в валюте счёта зачисления обязательна для разных валют; для одной валюты по умолчанию равна amount"`
	Tags          string   `json:"tags,omitempty" jsonschema:"теги через запятую"`
}

type writeTransactionOut struct {
	ID     int64  `json:"id"`
	Result string `json:"result"`
}

type deleteTransactionOut struct {
	Deleted bool  `json:"deleted"`
	ID      int64 `json:"id"`
}

type getReportIn struct {
	From string `json:"from" jsonschema:"начало периода YYYY-MM-DD, включительно"`
	To   string `json:"to" jsonschema:"конец периода YYYY-MM-DD, включительно"`
}

type currencyRateOut struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Rate     float64 `json:"rate"`
	Change   float64 `json:"change"`
	Source   string  `json:"source"`
	RateDate string  `json:"rate_date"`
}

type currencyRatesOut struct {
	UpdatedAt string            `json:"updated_at"`
	Rates     []currencyRateOut `json:"rates"`
}

type updateTransactionMetadataIn struct {
	ID          int64  `json:"id" jsonschema:"ID существующей транзакции"`
	Date        string `json:"date" jsonschema:"дата YYYY-MM-DD"`
	Description string `json:"description" jsonschema:"описание"`
	Tags        string `json:"tags" jsonschema:"теги через запятую; пустая строка очищает теги"`
}
