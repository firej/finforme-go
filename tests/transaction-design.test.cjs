const test = require('node:test');
const assert = require('node:assert/strict');
const period = require('../static/transaction-period.js');
const kind = require('../static/transaction-kind.js');
test('calendar ranges include leap day and cross year boundaries', () => {
  assert.equal(period.bounds('month', '2024-02').from, '2024-02-01');
  assert.equal(period.bounds('month', '2024-02').to, '2024-03-01');
  assert.match(period.bounds('month', '2024-02').hint, /29\.02\.2024/);
  assert.equal(period.bounds('quarter', '2026-12').from, '2026-10-01');
  assert.equal(period.bounds('quarter', '2026-12').to, '2027-01-01');
  assert.equal(period.bounds('year', '2026-09').from, '2026-01-01');
  assert.equal(period.shiftMonth('2026-01', -1), '2025-12');
  assert.equal(period.shiftMonth('2026-12', 3), '2027-03');
  assert.equal(period.bounds('all', '2026-09').from, '');
});
test('existing operations are classified without treating refunds as spending', () => {
  assert.equal(kind.infer('BANK', 'EXPENSE'), 'expense');
  assert.equal(kind.infer('INCOME', 'BANK'), 'income');
  assert.equal(kind.infer('BANK', 'LIABILITY'), 'transfer');
  assert.equal(kind.infer('EXPENSE', 'BANK'), 'other');
  assert.equal(kind.infer('EQUITY', 'BANK'), 'other');
  assert.equal(kind.allowed('expense','to','INCOME'), false);
  assert.equal(kind.allowed('income','from','INCOME'), true);
  assert.equal(kind.allowed('other','from','EXPENSE'), true);
});
