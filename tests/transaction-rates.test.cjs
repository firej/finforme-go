const {test} = require('node:test');
const assert = require('node:assert/strict');
const {attach, convert} = require('../static/transaction-rates.js');

function element(value = '') {
  return {value, dataset: {}, hidden: false, disabled: false, required: false, children: [], listeners: {},
    addEventListener(name, fn) { this.listeners[name] = fn; },
    fire(name) { return this.listeners[name](); },
    appendChild(node) { this.children.push(node); },
    replaceChildren() { this.children = []; this.value = ''; }
  };
}
function fixture(existing = false) {
  const fields = Object.fromEntries(['credit_account','debit_account','value','value_target','post_date'].map(n => [n, element()]));
  fields.credit_account.value = '3'; fields.debit_account.value = '2';
  fields.credit_account.selectedOptions = [{dataset: {commodityId: '2', commoditySign: 'USD'}}];
  fields.debit_account.selectedOptions = [{dataset: {commodityId: '1', commoditySign: 'RUB'}}];
  fields.value.value = '2.00'; fields.post_date.value = '2026-09-06';
  fields.value_target.value = existing ? '180.00' : '';
  const attrs = Object.fromEntries(['rate-panel','rate-choice','rate-status','rate-reset','target-group'].map(n => [n, element()]));
  const form = {dataset: {existing: String(existing)}, isConnected: true,
    ownerDocument: {createElement: () => element()},
    querySelector(selector) {
      const name = selector.match(/^\[name="(.*)"\]$/);
      return name ? fields[name[1]] : attrs[selector.slice(6, -1)];
    }
  };
  return {form, fields, attrs};
}
const rate = {code: 'USD/RUB', source: 'cbr', rate: '90.123456', date: '2026-09-04', inverse: false};
const response = (rates) => ({ok: true, json: async () => ({rates})});

test('exact decimal conversion, inverse and half-up rounding', () => {
  assert.equal(convert('2', '90.123456', false), '180.25');
  assert.equal(convert('100', '3.000000', true), '33.33');
  assert.equal(convert('1.01', '0.500000', false), '0.51');
  assert.equal(convert('0.01', '2', true), '0.01');
  for (const input of ['0', '-1', '1.001', 'Infinity', '1e3', '']) assert.equal(convert(input, '2', false), '');
  assert.equal(convert('2', '0', true), '');
  assert.equal(convert('90071992547409.91', '2', false), '');
});

test('new operation auto-calculates; manual amount survives edits until reset', async () => {
  const {form, fields, attrs} = fixture();
  await attach(form, async () => response([rate]));
  assert.equal(fields.value_target.value, '180.25');
  fields.value_target.value = '175.00'; fields.value_target.fire('input');
  fields.value.value = '3'; fields.value.fire('input');
  fields.post_date.value = '2026-09-05'; await fields.post_date.fire('change');
  assert.equal(fields.value_target.value, '175.00');
  assert.equal(attrs['rate-reset'].hidden, false);
  attrs['rate-reset'].fire('click');
  assert.equal(fields.value_target.value, '270.37');
});

test('existing amount is preserved and same-currency fields are excluded from submission', async () => {
  const {form, fields, attrs} = fixture(true);
  await attach(form, async () => response([rate]));
  assert.equal(fields.value_target.value, '180.00');
  assert.equal(attrs['rate-reset'].hidden, false);
  fields.debit_account.selectedOptions[0].dataset.commodityId = '2';
  await fields.debit_account.fire('change');
  assert.equal(fields.value_target.disabled, true);
  assert.equal(fields.value_target.required, false);
  assert.equal(attrs['rate-panel'].hidden, true);
  fields.debit_account.selectedOptions[0].dataset.commodityId = '1';
  await fields.debit_account.fire('change');
  assert.equal(fields.value_target.value, '180.00');
  assert.equal(fields.value_target.disabled, false);
});

test('multiple sources need a selection; no quote clears only automatic amounts', async () => {
  const {form, fields, attrs} = fixture();
  let rates = [rate, {...rate, source: 'other', rate: '95'}];
  await attach(form, async () => response(rates));
  assert.equal(fields.value_target.value, '');
  attrs['rate-choice'].value = JSON.stringify(['USD/RUB','other']);
  attrs['rate-choice'].fire('change');
  assert.equal(fields.value_target.value, '190.00');
  rates = []; await fields.post_date.fire('change');
  assert.equal(fields.value_target.value, '');
  fields.value_target.value = '191'; fields.value_target.fire('input');
  await fields.post_date.fire('change');
  assert.equal(fields.value_target.value, '191');
});

test('out-of-order responses cannot overwrite the current date or manual input', async () => {
  const {form, fields} = fixture();
  const pending = [];
  const fetcher = () => new Promise(resolve => pending.push(resolve));
  const first = attach(form, fetcher);
  fields.post_date.value = '2026-09-07';
  const second = fields.post_date.fire('change');
  pending[1](response([{...rate, rate: '99'}])); await second;
  assert.equal(fields.value_target.value, '198.00');
  pending[0](response([rate])); await first;
  assert.equal(fields.value_target.value, '198.00');
  const third = fields.post_date.fire('change');
  fields.value_target.value = '197'; fields.value_target.fire('input');
  pending[2](response([rate])); await third;
  assert.equal(fields.value_target.value, '197');
});

test('network failure keeps manual input and leaves automatic input empty', async () => {
  for (const existing of [false, true]) {
    const {form, fields, attrs} = fixture(existing);
    await attach(form, async () => {throw new Error('offline');});
    assert.equal(fields.value_target.value, existing ? '180.00' : '');
    assert.match(attrs['rate-status'].textContent, /Не удалось/);
  }
});
