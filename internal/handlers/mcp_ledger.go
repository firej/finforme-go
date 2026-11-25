package handlers

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"time"
)

func (h *Handler) addLedgerTools(server *mcp.Server, userID int64) {
	mcp.AddTool(server, &mcp.Tool{Name: "list_transaction_history", Description: "История создания, изменения и удаления операций: состояния до и после, источник и ID записи для восстановления. По 50 записей; before — ID последней записи предыдущей страницы."}, func(ctx context.Context, req *mcp.CallToolRequest, in struct {
		TransactionID int64 `json:"transaction_id,omitempty"`
		Before        int64 `json:"before,omitempty"`
	}) (*mcp.CallToolResult, struct {
		History []transactionHistory `json:"history"`
	}, error) { items, err := h.transactionHistory(userID, in.TransactionID, in.Before); return nil, struct {
		History []transactionHistory `json:"history"`
	}{items}, err })
	mcp.AddTool(server, &mcp.Tool{Name: "restore_transaction", Description: "Восстановить удалённую операцию по ID записи истории удаления. Повторный вызов не создаёт дубль; возвращает ID восстановленной операции."}, func(ctx context.Context, req *mcp.CallToolRequest, in struct {
		HistoryID int64 `json:"history_id"`
	}) (*mcp.CallToolResult, writeTransactionOut, error) { id, err := h.restoreDeletedTransaction(userID, in.HistoryID, "mcp"); return nil, writeTransactionOut{ID: id, Result: "ok"}, err })
	mcp.AddTool(server, &mcp.Tool{Name: "reconcile_account", Description: "Сравнить остаток счёта с выпиской на конец date. expected — точная десятичная строка; задолженность со знаком минус. save=true сохраняет точку сверки только при точном совпадении; проводки не изменяются."}, func(ctx context.Context, req *mcp.CallToolRequest, in struct {
		AccountID int64  `json:"account_id"`
		Date      string `json:"date"`
		Expected  string `json:"expected"`
		Save      bool   `json:"save,omitempty"`
	}) (*mcp.CallToolResult, reconciliationResult, error) { date, err := time.Parse("2006-01-02", in.Date); if err != nil {
		return nil, reconciliationResult{}, validationError("Некорректная дата")
	}; out, err := h.reconcileAccount(userID, in.AccountID, date, in.Expected, in.Save); return nil, out, err })
	mcp.AddTool(server, &mcp.Tool{Name: "mark_transaction_checked", Description: "Поставить или снять отметку проверки операции по выписке. Изменение операции автоматически снимает отметку."}, func(ctx context.Context, req *mcp.CallToolRequest, in struct {
		ID      int64 `json:"id"`
		Checked bool  `json:"checked"`
	}) (*mcp.CallToolResult, writeTransactionOut, error) { err := h.setTransactionChecked(userID, in.ID, in.Checked); return nil, writeTransactionOut{ID: in.ID, Result: "ok"}, err })
}
