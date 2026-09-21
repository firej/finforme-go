(function(root) {
  'use strict';
  var moneyAccounts = ['ASSET', 'BANK', 'CASH', 'LIABILITY'];
  function allowed(kind, side, type) {
    if (kind === 'other') return true;
    if (kind === 'expense' && side === 'to') return type === 'EXPENSE';
    if (kind === 'income' && side === 'from') return type === 'INCOME';
    return moneyAccounts.includes(type);
  }
  function infer(from, to) {
    if (allowed('expense', 'from', from) && to === 'EXPENSE') return 'expense';
    if (from === 'INCOME' && allowed('income', 'to', to)) return 'income';
    if (moneyAccounts.includes(from) && moneyAccounts.includes(to)) return 'transfer';
    return 'other';
  }
  function attach(form) {
    if (!form || form.kindController) return;
    var from = form.querySelector('[name="credit_account"]');
    var to = form.querySelector('[name="debit_account"]');
    if (!from || !to) return; // Complex operations keep their metadata-only form.
    var buttons = form.querySelectorAll('[data-transaction-kind]');
    function type(select) { return select.selectedOptions[0] ? select.selectedOptions[0].dataset.accountType : ''; }
    function filter(select, kind, side) {
      var previous = select.value;
      Array.from(select.options).forEach(function(option) {
        var visible = !option.value || allowed(kind, side, option.dataset.accountType);
        option.hidden = !visible;
        option.disabled = !visible;
      });
      if (select.selectedOptions[0] && select.selectedOptions[0].disabled) select.value = '';
      return previous !== select.value;
    }
    function apply(kind) {
      var labels = {expense:['Счёт', 'Категория расхода'], income:['Источник дохода', 'Счёт'], transfer:['Откуда', 'Куда'], other:['Счёт списания', 'Счёт зачисления']}[kind];
      form.querySelector('label[for="modal-credit_account"]').textContent = labels[0];
      form.querySelector('label[for="modal-debit_account"]').textContent = labels[1];
      buttons.forEach(function(button) { button.setAttribute('aria-pressed', String(button.dataset.transactionKind === kind)); });
      var changedFrom = filter(from, kind, 'from');
      var changedTo = filter(to, kind, 'to');
      if (from.accountPicker) from.accountPicker.refresh();
      if (to.accountPicker) to.accountPicker.refresh();
      if ((changedFrom || changedTo) && form.rateController) form.rateController.refresh();
    }
    var kind = infer(type(from), type(to));
    if (form.dataset.existing !== 'true') {
      var currentID = form.querySelector('[name="account_id"]').value;
      var current = Array.from(from.options).find(function(option) { return option.value === currentID; });
      var currentType = current ? current.dataset.accountType : '';
      kind = currentType === 'INCOME' ? 'income' : currentType === 'EQUITY' ? 'other' : 'expense';
      if (currentType === 'EXPENSE') { to.value = currentID; from.value = ''; }
    }
    buttons.forEach(function(button) { button.addEventListener('click', function() { apply(button.dataset.transactionKind); }); });
    apply(kind);
    form.kindController = {apply:apply};
  }
  root.TransactionKind = {attach:attach, infer:infer, allowed:allowed};
  if (typeof module !== 'undefined' && module.exports) module.exports = root.TransactionKind;
})(typeof window !== 'undefined' ? window : globalThis);
