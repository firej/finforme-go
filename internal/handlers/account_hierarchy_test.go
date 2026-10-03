package handlers

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestFinanceNestedAccountEditing(t *testing.T) {
	h := financeTestHandler(t)
	for _, q := range []string{
		`UPDATE accounts SET name='Расходы' WHERE id=6`,
		`UPDATE accounts SET name='Автомобиль', parent_id=6 WHERE id=2`,
		`UPDATE accounts SET name='Ремонт', parent_id=2 WHERE id=4`,
		`UPDATE accounts SET parent_id=4 WHERE id=3`,
	} {
		if _, err := h.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	txID, err := h.saveTransaction(2, validFinanceInput())
	if err != nil {
		t.Fatal(err)
	}
	before := splitSnapshot(t, h, txID)
	cookie := authCookie(t, h, 2)
	for _, drawer := range []bool{false, true} {
		for _, id := range []int{2, 4, 3} {
			w := httptest.NewRecorder()
			r := authRequest("GET", fmt.Sprintf("/api/v1/finance/account/form?account_id=%d", id), nil, cookie)
			if drawer {
				h.APIAccountFormGet(w, r)
			} else {
				r = mux.SetURLVars(r, map[string]string{"id": fmt.Sprint(id)})
				h.FinanceAccountEdit(w, r)
			}
			if w.Code != 200 {
				t.Fatalf("form %d: %d %s", id, w.Code, w.Body.String())
			}
			selectHTML := regexp.MustCompile(`(?s)<select[^>]*name="account_parent"[^>]*>(.*?)</select>`).FindString(w.Body.String())
			parent := map[int]int{2: 6, 4: 2, 3: 4}[id]
			if !regexp.MustCompile(fmt.Sprintf(`value="%d"\s+selected`, parent)).MatchString(selectHTML) {
				t.Fatalf("drawer=%v account=%d: parent missing or not selected: %s", drawer, id, selectHTML)
			}
			for _, excluded := range map[int][]int{2: {2, 4, 3}, 4: {4, 3}, 3: {3}}[id] {
				if strings.Contains(selectHTML, fmt.Sprintf(`value="%d"`, excluded)) {
					t.Fatalf("self/descendant offered: %s", selectHTML)
				}
			}
		}
	}
	values := url.Values{"id": {"2"}, "account_name": {"Автомобиль обновлён"}, "account_type": {"EXPENSE"}, "commodity_id": {"1"}, "account_parent": {"6"}}
	w := httptest.NewRecorder()
	h.APIAccountSave(w, authRequest("POST", "/api/v1/finance/account/save", values, cookie))
	if w.Code != 200 {
		t.Fatalf("editing regular parent: %d %s", w.Code, w.Body.String())
	}
	for _, parent := range []string{"2", "4", "3", "5", "invalid"} {
		values.Set("account_parent", parent)
		w = httptest.NewRecorder()
		h.APIAccountSave(w, authRequest("POST", "/api/v1/finance/account/save", values, cookie))
		if w.Code != 400 {
			t.Fatalf("invalid parent %s accepted: %d", parent, w.Code)
		}
	}
	if got := splitSnapshot(t, h, txID); !reflect.DeepEqual(before, got) {
		t.Fatal("account edit changed transaction splits")
	}
	var parent int64
	var name string
	if err := h.db.QueryRow(`SELECT parent_id,name FROM accounts WHERE id=2`).Scan(&parent, &name); err != nil {
		t.Fatal(err)
	}
	if parent != 6 || name != "Автомобиль обновлён" {
		t.Fatalf("unexpected saved account: %d %s", parent, name)
	}
	// MCP supports the same hierarchy, including editing an ordinary parent.
	newParent := int64(4)
	if _, err := h.mutateMCPAccount(2, 1, updateAccountIn{ParentID: &newParent}); err != nil {
		t.Fatal(err)
	}
	name = "Автомобиль MCP"
	if _, err := h.mutateMCPAccount(2, 2, updateAccountIn{Name: &name}); err != nil {
		t.Fatal(err)
	}
	newParent = 1
	if _, err := h.mutateMCPAccount(2, 2, updateAccountIn{ParentID: &newParent}); err == nil {
		t.Fatal("MCP accepted descendant as parent")
	}
}

func TestFinanceDeepParentValidation(t *testing.T) {
	h := financeTestHandler(t)
	parent := int64(6)
	for id := int64(100); id < 205; id++ {
		if _, err := h.db.Exec(`INSERT INTO accounts(id,user_id,name,account_type,commodity_id,commodity_scu,non_std_scu,parent_id) VALUES(?,2,'Nested','EXPENSE',1,100,0,?)`, id, parent); err != nil {
			t.Fatal(err)
		}
		parent = id
	}
	if err := validateParentAccount(h.db, 2, 1, parent); err != nil {
		t.Fatalf("deep valid hierarchy rejected: %v", err)
	}
	if err := validateParentAccount(h.db, 2, 6, parent); err == nil {
		t.Fatal("deep cycle accepted")
	}
	if _, err := h.db.Exec(`UPDATE accounts SET parent_id=204 WHERE id=100`); err != nil {
		t.Fatal(err)
	}
	if err := validateParentAccount(h.db, 2, 1, parent); err == nil {
		t.Fatal("existing cycle accepted")
	}
}
