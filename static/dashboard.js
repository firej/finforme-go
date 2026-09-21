(function() {
  'use strict';
  var form = document.getElementById('dashboard-options');
  if (!form) return;
  form.querySelectorAll('[data-dashboard-submit]').forEach(function(control) {
    control.addEventListener('change', function() {
      if (control.name === 'month') form.action = '/#monthly-report';
      form.requestSubmit();
    });
  });
  var historyScroll = form.querySelector('.history-chart-scroll');
  var selectedMonth = form.querySelector('.history-month.history-selected');
  if (historyScroll && selectedMonth) {
    historyScroll.scrollLeft = selectedMonth.offsetLeft - (historyScroll.clientWidth - selectedMonth.offsetWidth) / 2;
  }
  var buttons = form.querySelectorAll('[data-report-currency]');
  var currencyInput = document.createElement('input');
  currencyInput.type = 'hidden'; currencyInput.name = 'report_currency';
  form.appendChild(currencyInput);
  function selectCurrency(currency, remember) {
    currencyInput.value = currency;
    form.querySelectorAll('.report-month-control a, [data-history-link]').forEach(function(link) {
      var url = new URL(link.href); url.searchParams.set('report_currency', currency); link.href = url.toString();
    });
    if (remember) {
      var url = new URL(window.location.href); url.searchParams.set('report_currency', currency);
      window.history.replaceState(null, '', url.toString());
    }
    buttons.forEach(function(button) { button.setAttribute('aria-pressed', String(button.dataset.reportCurrency === currency)); });
    form.querySelectorAll('[data-report-panel]').forEach(function(panel) { panel.hidden = panel.dataset.reportPanel !== currency; });
  }
  buttons.forEach(function(button) { button.addEventListener('click', function() { selectCurrency(button.dataset.reportCurrency, true); }); });
  if (buttons.length) {
    var preferred = new URLSearchParams(window.location.search).get('report_currency');
    var selected = Array.from(buttons).find(function(button) { return button.dataset.reportCurrency === preferred; });
    selectCurrency((selected || buttons[0]).dataset.reportCurrency, false);
  }
})();
