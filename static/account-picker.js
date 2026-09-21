(function(root) {
  'use strict';
  function normalize(text) { return text.toLocaleLowerCase('ru').replace(/ё/g, 'е').trim(); }
  function matches(text, query) {
    var name = normalize(text);
    return normalize(query).split(/\s+/).every(function(word) { return name.includes(word); });
  }
  function attach(form) {
    if (!form) return;
    form.querySelectorAll('select[name="credit_account"], select[name="debit_account"]').forEach(function(select) {
      if (select.accountPicker) return;
      var doc = select.ownerDocument, id = select.id;
      var wrapper = doc.createElement('div');
      wrapper.className = 'account-picker';
      select.parentNode.insertBefore(wrapper, select);
      wrapper.appendChild(select);
      select.id = id + '-native';
      select.hidden = true;
      var input = doc.createElement('input');
      input.id = id;
      input.type = 'text';
      input.className = 'form-input account-picker-input';
      input.autocomplete = 'off';
      input.placeholder = 'Найти счёт…';
      input.required = select.required;
      input.setAttribute('role', 'combobox');
      input.setAttribute('aria-autocomplete', 'list');
      input.setAttribute('aria-expanded', 'false');
      input.setAttribute('aria-controls', id + '-options');
      wrapper.appendChild(input);
      var list = doc.createElement('div');
      list.id = id + '-options';
      list.className = 'account-picker-options';
      list.setAttribute('role', 'listbox');
      list.hidden = true;
      wrapper.appendChild(list);
      var empty = doc.createElement('div');
      empty.className = 'account-picker-empty';
      empty.setAttribute('role', 'status');
      empty.textContent = 'Счёт не найден';
      var choices = [], active = -1, open = false;
      function selectedText() {
        var option = select.selectedOptions[0];
        return option && option.value && !option.disabled ? option.textContent.trim() : '';
      }
      function restore() { input.value = selectedText(); input.setCustomValidity(''); }
      function close() {
        open = false; list.hidden = true;
        input.setAttribute('aria-expanded', 'false');
        input.removeAttribute('aria-activedescendant');
        restore();
      }
      function highlight(index) {
        active = index;
        Array.from(list.children).forEach(function(node, i) { node.setAttribute('aria-selected', String(i === active)); });
        if (active >= 0 && list.children[active]) {
          input.setAttribute('aria-activedescendant', list.children[active].id);
          list.children[active].scrollIntoView({block:'nearest'});
        } else input.removeAttribute('aria-activedescendant');
      }
      function choose(option) {
        select.value = option.value;
        select.dispatchEvent(new Event('change', {bubbles:true}));
        close();
      }
      function render(query) {
        choices = Array.from(select.options).filter(function(option) { return option.value && !option.hidden && !option.disabled && matches(option.textContent, query); });
        list.replaceChildren();
        choices.forEach(function(option, index) {
          var item = doc.createElement('div');
          item.id = id + '-option-' + index;
          item.className = 'account-picker-option';
          item.setAttribute('role', 'option');
          item.textContent = option.textContent.trim();
          item.addEventListener('pointerdown', function(event) { event.preventDefault(); });
          item.addEventListener('click', function() { choose(option); });
          list.appendChild(item);
        });
        if (!choices.length) list.appendChild(empty);
        highlight(-1);
      }
      function show(query) {
        open = true; list.hidden = false;
        input.setAttribute('aria-expanded', 'true');
        render(query);
      }
      input.addEventListener('focus', function() { show(''); input.select(); });
      input.addEventListener('click', function() { if (!open) show(''); });
      input.addEventListener('input', function() {
        input.setCustomValidity('Выберите счёт из списка.');
        show(input.value);
      });
      input.addEventListener('blur', close);
      input.addEventListener('keydown', function(event) {
        if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
          event.preventDefault();
          if (!open) show('');
          if (choices.length) highlight((active + (event.key === 'ArrowDown' ? 1 : active < 0 ? 0 : -1) + choices.length) % choices.length);
        } else if (event.key === 'Enter' && open) {
          event.preventDefault();
          if (active >= 0) choose(choices[active]);
          else if (choices.length === 1) choose(choices[0]);
        } else if (event.key === 'Escape' && open) {
          event.preventDefault(); event.stopPropagation(); close();
        } else if (event.key === 'Tab') close();
      });
      select.addEventListener('change', restore);
      select.addEventListener('invalid', function(event) { event.preventDefault(); input.focus(); input.reportValidity(); });
      select.accountPicker = {refresh:close};
      restore();
    });
  }
  root.AccountPicker = {attach:attach, matches:matches};
  if (typeof module !== 'undefined' && module.exports) module.exports = root.AccountPicker;
})(typeof window !== 'undefined' ? window : globalThis);
