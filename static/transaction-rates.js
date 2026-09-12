(function (root) {
  'use strict';
  // All arithmetic is in integer cents and the exact stored decimal rate.
  function convert(amount, rate, inverse) {
    if (!/^\d+(?:\.\d{0,2})?$/.test(amount) || !/^\d+(?:\.\d{1,6})?$/.test(rate)) return '';
    var a = amount.split('.'), r = rate.split('.');
    var cents = BigInt(a[0]) * 100n + BigInt((a[1] || '').padEnd(2, '0'));
    var scale = 10n ** BigInt((r[1] || '').length);
    var numerator = BigInt(r[0]) * scale + BigInt(r[1] || '0');
    if (cents <= 0n || cents > 9007199254740991n || numerator <= 0n) return '';
    var divisor = inverse ? numerator : scale;
    var product = cents * (inverse ? scale : numerator);
    var result = (product * 2n + divisor) / (divisor * 2n);
    if (result <= 0n || result > 9007199254740991n) return '';
    return (result / 100n).toString() + '.' + (result % 100n).toString().padStart(2, '0');
  }

  function attach(form, fetcher) {
    if (!form || form.rateController) return;
    var find = function (name) { return form.querySelector('[name="' + name + '"]'); };
    var from = find('credit_account'), to = find('debit_account');
    var amount = find('value'), target = find('value_target'), date = find('post_date');
    var panel = form.querySelector('[data-rate-panel]');
    if (!from || !to || !target || !panel) return; // Metadata-only operations.
    var choices = form.querySelector('[data-rate-choice]');
    var status = form.querySelector('[data-rate-status]');
    var reset = form.querySelector('[data-rate-reset]');
    var targetGroup = form.querySelector('[data-target-group]');
    var manual = form.dataset.existing === 'true';
    var rates = [], generation = 0, selection = '', different = false;
    fetcher = fetcher || root.fetch.bind(root);
    function identity(rate) { return JSON.stringify([rate.code, rate.source]); }
    function selected() { return rates.find(function (r) { return identity(r) === choices.value; }); }
    function option(value, text) {
      var node = form.ownerDocument.createElement('option');
      node.value = value; node.textContent = text;
      choices.appendChild(node);
    }
    function calculate() {
      if (!different) return;
      var rate = selected();
      reset.hidden = !manual || !rate;
      if (!rate) return;
      if (manual) {
        status.textContent = 'Сумма сохранена или введена вручную. Для замены нажмите «Пересчитать по курсу».';
        return;
      }
      target.value = convert(amount.value, rate.rate, rate.inverse);
      status.textContent = target.value ? 'Сумма рассчитана по выбранному курсу.' : 'Введите сумму для расчёта. Результат должен быть не меньше 0,01.';
    }
    async function refresh() {
      var request = ++generation;
      var fromOption = from.selectedOptions[0], toOption = to.selectedOptions[0];
      different = !!(fromOption && toOption && fromOption.dataset.commodityId && toOption.dataset.commodityId && fromOption.dataset.commodityId !== toOption.dataset.commodityId);
      target.disabled = !different;
      target.required = different;
      if (targetGroup) targetGroup.hidden = !different;
      panel.hidden = !different;
      var fromSign = form.querySelector('[data-from-sign]'), toSign = form.querySelector('[data-to-sign]');
      if (fromSign) fromSign.textContent = fromOption ? fromOption.dataset.commoditySign || '' : '';
      if (toSign) toSign.textContent = toOption ? toOption.dataset.commoditySign || '' : '';
      rates = []; choices.replaceChildren(); choices.disabled = true; reset.hidden = true;
      if (!manual) target.value = '';
      if (!different) return;
      if (!date.value) { status.textContent = 'Укажите дату операции.'; return; }
      status.textContent = 'Загрузка курсов…';
      var query = new URLSearchParams({credit_account: from.value, debit_account: to.value, post_date: date.value});
      try {
        var response = await fetcher('/api/v1/finance/transaction/rates?' + query.toString(), {headers: {Accept: 'application/json'}});
        if (!response.ok) throw new Error('rates');
        var data = await response.json();
        if (request !== generation || !form.isConnected) return;
        rates = data.rates;
        choices.replaceChildren();
        option('', rates.length ? 'Выберите источник курса' : 'Курс не найден');
        rates.forEach(function (rate) {
          option(identity(rate), rate.code + ' · ' + rate.source + ' · ' + rate.rate + ' · ' + rate.date + (rate.inverse ? ' · обратный курс' : ''));
        });
        choices.disabled = rates.length === 0;
        if (rates.some(function (r) { return identity(r) === selection; })) choices.value = selection;
        else if (rates.length === 1) choices.value = identity(rates[0]);
        else choices.value = '';
        selection = choices.value;
        status.textContent = rates.length ? 'Выберите источник курса для расчёта.' : 'Нет привязанного курса за последние 14 дней на дату операции. Введите сумму вручную.';
        calculate();
      } catch (error) {
        if (request !== generation || !form.isConnected) return;
        status.textContent = 'Не удалось загрузить курс. Введите сумму вручную.';
      }
    }
    from.addEventListener('change', refresh);
    to.addEventListener('change', refresh);
    date.addEventListener('change', refresh);
    amount.addEventListener('input', calculate);
    target.addEventListener('input', function () { manual = true; calculate(); });
    choices.addEventListener('change', function () {
      selection = choices.value;
      if (!manual) target.value = '';
      status.textContent = 'Выберите источник курса для расчёта.';
      calculate();
    });
    reset.addEventListener('click', function () { manual = false; calculate(); });
    form.rateController = {refresh: refresh};
    return refresh();
  }
  root.TransactionRates = {attach: attach, convert: convert};
  if (typeof module !== 'undefined' && module.exports) module.exports = root.TransactionRates;
})(typeof window !== 'undefined' ? window : globalThis);
