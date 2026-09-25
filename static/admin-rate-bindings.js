document.querySelectorAll('.rate-binding-form').forEach(form => {
  const base = form.elements.base_id;
  const quote = form.elements.quote_id;
  const example = form.querySelector('.rate-binding-example');
  const warning = form.querySelector('.rate-binding-warning');
  function update() {
    const from = base.selectedOptions[0]?.dataset.currency;
    const to = quote.selectedOptions[0]?.dataset.currency;
    const amount = form.dataset.exampleAmount;
    example.textContent = !amount
      ? 'Нет котировки для примера пересчёта.'
      : from && to
        ? `100 ${from} = ${amount} ${to}`
        : 'Выберите обе валюты, чтобы увидеть пример пересчёта.';
    const pair = form.elements.code.value;
    warning.hidden = !from || !to || `${from}/${to}`.toUpperCase() === pair.toUpperCase();
    warning.textContent = `Выбранные валюты или их порядок не соответствуют паре ${pair}. Проверьте выбор перед сохранением.`;
  }
  base.addEventListener('change', update);
  quote.addEventListener('change', update);
  update();
});
