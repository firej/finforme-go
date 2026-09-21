const test = require('node:test');
const assert = require('node:assert/strict');
const {matches} = require('../static/account-picker.js');
test('account search ignores case, indentation and ё, matching all words in any order', () => {
  assert.equal(matches('    Расчётный счёт Альфа', 'альфа расчет'), true);
  assert.equal(matches('Продукты / Супермаркеты', 'прод суп'), true);
  assert.equal(matches('Расчётный счёт Альфа', 'альфа наличные'), false);
  assert.equal(matches('Наличные', '  '), true);
});
