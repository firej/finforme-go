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

  form.querySelectorAll('[data-spending-trend]').forEach(function(figure) {
    var plot = figure.querySelector('[data-spending-plot]');
    var slider = figure.querySelector('[data-spending-day]');
    var cursor = figure.querySelector('[data-spending-cursor]');
    var tooltip = figure.querySelector('[data-spending-tooltip]');
    var template = figure.querySelector('[data-spending-points]');
    var points = template.content.children;
    var labels = template.dataset;

    function update(day, show) {
      var point = points[day - 1].dataset;
      var title = day + '-й день месяца';
      function describe(month, value) {
        return month + ': ' + (value === '—' ? value : value + ' ' + labels.currency);
      }
      var comparison = [title, describe(labels.currentMonth, point.current), describe(labels.previousMonth, point.previous)];
      slider.value = day;
      slider.setAttribute('aria-valuetext', comparison.join('. '));
      cursor.style.left = (100 * day / points.length) + '%';
      tooltip.textContent = comparison.join('\n');
      cursor.hidden = tooltip.hidden = !show;
      if (show) {
        var x = plot.clientWidth * day / points.length;
        var xTooltip = Math.max(0, Math.min(x - tooltip.offsetWidth / 2, plot.clientWidth - tooltip.offsetWidth));
        tooltip.style.left = xTooltip + 'px';
      }
    }

    function showAtPointer(event) {
      var rect = plot.getBoundingClientRect();
      var day = Math.round((event.clientX - rect.left) / rect.width * points.length);
      update(Math.max(1, Math.min(day, points.length)), true);
    }

    function hide() { cursor.hidden = tooltip.hidden = true; }
    plot.addEventListener('pointermove', function(event) {
      if (event.pointerType === 'mouse') showAtPointer(event);
    });
    plot.addEventListener('pointerdown', showAtPointer);
    plot.addEventListener('pointerleave', function(event) {
      if (event.pointerType === 'mouse' && document.activeElement !== slider) hide();
    });
    plot.addEventListener('pointercancel', hide);
    slider.addEventListener('focus', function() { update(slider.valueAsNumber, true); });
    slider.addEventListener('input', function() { update(slider.valueAsNumber, true); });
    slider.addEventListener('blur', hide);
    slider.addEventListener('keydown', function(event) { if (event.key === 'Escape') hide(); });
    update(slider.valueAsNumber, false);
    slider.hidden = false;
  });
})();
