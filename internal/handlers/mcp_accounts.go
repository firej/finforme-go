package handlers

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type accountIDIn struct {
	ID int64 `json:"id" jsonschema:"ID существующего счёта"`
}

type createAccountIn struct {
	Name        string `json:"name"`
	AccountType string `json:"account_type" jsonschema:"ASSET, CASH, BANK, LIABILITY, INCOME, EXPENSE или EQUITY"`
	CommodityID int64  `json:"commodity_id" jsonschema:"ID валюты из list_commodities"`
	ParentID    int64  `json:"parent_id,omitempty" jsonschema:"ID контейнера; 0 — без родителя"`
	Description string `json:"description,omitempty"`
	Hidden      bool   `json:"hidden,omitempty"`
	Placeholder bool   `json:"placeholder,omitempty" jsonschema:"контейнер для дочерних счетов, без операций"`
}

type updateAccountIn struct {
	ID          int64   `json:"id"`
	Name        *string `json:"name,omitempty"`
	AccountType *string `json:"account_type,omitempty"`
	CommodityID *int64  `json:"commodity_id,omitempty"`
	ParentID    *int64  `json:"parent_id,omitempty" jsonschema:"0 — убрать родителя; пропущенное поле сохраняет родителя"`
	Description *string `json:"description,omitempty"`
	Hidden      *bool   `json:"hidden,omitempty" jsonschema:"true — скрыть, false — показать"`
	Placeholder *bool   `json:"placeholder,omitempty"`
}

func (h *Handler) addAccountTools(server *mcp.Server, userID int64) {
	mcp.AddTool(server, &mcp.Tool{Name: "list_commodities", Description: "Доступные валюты и их ID для создания и редактирования счетов."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
			items, err := h.getCommodities()
			return nil, map[string]any{"commodities": items}, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "get_account", Description: "Параметры счёта, включая parent_id, commodity_id, описание и признаки скрытого/контейнерного счёта."},
		func(ctx context.Context, req *mcp.CallToolRequest, in accountIDIn) (*mcp.CallToolResult, any, error) {
			a, err := h.getAccountByID(userID, in.ID)
			if errors.Is(err, sql.ErrNoRows) {
				err = validationError("Счёт не найден")
			}
			return nil, a, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "create_account", Description: "Создать счёт или категорию доходов/расходов. Сначала получите commodity_id через list_commodities. Родитель должен быть контейнером. Начальный баланс задаётся отдельной транзакцией."},
		func(ctx context.Context, req *mcp.CallToolRequest, in createAccountIn) (*mcp.CallToolResult, any, error) {
			id, err := h.mutateMCPAccount(userID, 0, updateAccountIn{Name: &in.Name, AccountType: &in.AccountType, CommodityID: &in.CommodityID, ParentID: &in.ParentID, Description: &in.Description, Hidden: &in.Hidden, Placeholder: &in.Placeholder})
			return nil, map[string]any{"id": id}, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "update_account", Description: "Частично изменить счёт: имя, описание, тип, валюту, родителя, hidden или placeholder. Пропущенные поля сохраняются. Валюту счёта с операциями менять нельзя. Системный ROOT менять нельзя."},
		func(ctx context.Context, req *mcp.CallToolRequest, in updateAccountIn) (*mcp.CallToolResult, any, error) {
			if in.ID <= 0 {
				return nil, nil, validationError("Укажите ID счёта")
			}
			id, err := h.mutateMCPAccount(userID, in.ID, in)
			return nil, map[string]any{"id": id}, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "delete_account", Description: "Необратимо удалить пустой счёт без операций и дочерних счетов. Для счёта с историей используйте update_account с hidden=true. Системный ROOT удалить нельзя."},
		func(ctx context.Context, req *mcp.CallToolRequest, in accountIDIn) (*mcp.CallToolResult, any, error) {
			err := h.deleteMCPAccount(userID, in.ID)
			return nil, map[string]any{"id": in.ID, "deleted": err == nil}, err
		})
}

// Read, merge and validate under the same per-user lock as other finance writes.
func (h *Handler) mutateMCPAccount(userID, id int64, in updateAccountIn) (int64, error) {
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var name, kind, description string
	var currency, oldCurrency int64
	var parent sql.NullInt64
	var hidden, placeholder bool
	if id != 0 {
		err = tx.QueryRow(`SELECT name, account_type, commodity_id, parent_id, COALESCE(description,''), hidden, placeholder FROM accounts WHERE id=? AND user_id=?`, id, userID).Scan(&name, &kind, &currency, &parent, &description, &hidden, &placeholder)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, validationError("Счёт не найден")
		}
		if err != nil {
			return 0, err
		}
		if kind == "ROOT" {
			return 0, validationError("Системный ROOT нельзя изменять")
		}
		oldCurrency = currency
	}
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if in.AccountType != nil {
		kind = *in.AccountType
	}
	if in.CommodityID != nil {
		currency = *in.CommodityID
	}
	if in.Description != nil {
		description = *in.Description
	}
	if in.Hidden != nil {
		hidden = *in.Hidden
	}
	if in.Placeholder != nil {
		placeholder = *in.Placeholder
	}
	if in.ParentID != nil {
		if *in.ParentID < 0 {
			return 0, validationError("Некорректный родитель")
		}
		parent = sql.NullInt64{Int64: *in.ParentID, Valid: *in.ParentID != 0}
	}
	if name == "" || utf8.RuneCountInString(name) > 255 {
		return 0, validationError("Имя счёта должно содержать от 1 до 255 символов")
	}
	switch kind {
	case "ASSET", "CASH", "BANK", "LIABILITY", "INCOME", "EXPENSE", "EQUITY":
	default:
		return 0, validationError("Недопустимый тип счёта")
	}
	var fraction int
	if err = tx.QueryRow(`SELECT fraction FROM commodities WHERE id=?`, currency).Scan(&fraction); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, validationError("Валюта не найдена: используйте list_commodities")
		}
		return 0, err
	}
	if parent.Valid {
		if err = validateParentAccount(tx, userID, id, parent.Int64); err != nil {
			return 0, err
		}
		var container bool
		var parentType string
		if err = tx.QueryRow(`SELECT placeholder, account_type FROM accounts WHERE id=? AND user_id=?`, parent.Int64, userID).Scan(&container, &parentType); err != nil {
			return 0, err
		}
		if !container && parentType != "ROOT" {
			return 0, validationError("Родитель должен быть контейнером")
		}
	}
	if id != 0 {
		var splits, kids int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM splits WHERE account_id=?`, id).Scan(&splits); err != nil {
			return 0, err
		}
		if err = tx.QueryRow(`SELECT COUNT(*) FROM accounts WHERE parent_id=?`, id).Scan(&kids); err != nil {
			return 0, err
		}
		if splits > 0 && currency != oldCurrency {
			return 0, validationError("Нельзя менять валюту счёта с операциями")
		}
		if splits > 0 && placeholder {
			return 0, validationError("Счёт с операциями нельзя сделать контейнером")
		}
		if kids > 0 && !placeholder {
			return 0, validationError("У счёта с дочерними счетами нельзя убрать признак контейнера")
		}
		_, err = tx.Exec(`UPDATE accounts SET name=?,account_type=?,commodity_id=?,parent_id=?,description=?,hidden=?,placeholder=? WHERE id=? AND user_id=?`, name, kind, currency, parent, description, hidden, placeholder, id, userID)
	} else {
		var result sql.Result
		result, err = tx.Exec(`INSERT INTO accounts(user_id,name,account_type,commodity_id,commodity_scu,non_std_scu,parent_id,description,hidden,placeholder) VALUES(?,?,?,?,100,0,?,?,?,?)`, userID, name, kind, currency, parent, description, hidden, placeholder)
		if err == nil {
			id, err = result.LastInsertId()
		}
	}
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (h *Handler) deleteMCPAccount(userID, id int64) error {
	if id <= 0 {
		return validationError("Укажите ID счёта")
	}
	tx, err := h.beginFinanceWrite(userID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind string
	if err = tx.QueryRow(`SELECT account_type FROM accounts WHERE id=? AND user_id=?`, id, userID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return validationError("Счёт не найден")
		}
		return err
	}
	if kind == "ROOT" {
		return validationError("Системный ROOT нельзя удалить")
	}
	for _, query := range []string{`SELECT COUNT(*) FROM splits WHERE account_id=?`, `SELECT COUNT(*) FROM accounts WHERE parent_id=?`, `SELECT COUNT(*) FROM books WHERE root_account_id=?`} {
		var count int
		if err = tx.QueryRow(query, id).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return validationError("Счёт содержит операции, дочерние счета или является корнем книги. Используйте hidden=true для скрытия")
		}
	}
	if _, err = tx.Exec(`DELETE FROM accounts WHERE id=? AND user_id=?`, id, userID); err != nil {
		return err
	}
	return tx.Commit()
}
