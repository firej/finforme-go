const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(require('node:path').join(__dirname, '../templates/finance_import_csv.html'), 'utf8');
const start = source.indexOf('function parseAmount(');
const end = source.indexOf('// ── Шаг 2', start);
const parseAmount = vm.runInNewContext('(' + source.slice(start, end).trim() + ')');
test('CSV keeps decimal input unchanged instead of rounding to cents', () => {
 assert.equal(parseAmount('0,00000001', 'auto', true), '0.00000001');
 assert.equal(parseAmount('90 071 992,54740991', 'auto', true), '90071992.54740991');
 assert.equal(parseAmount('-1.23456789', '.', true), '-1.23456789');
 assert.equal(parseAmount('0.00000001', '.'), 0.00000001);
 assert.ok(Number.isNaN(parseAmount('1-2', '.', true)));
});
