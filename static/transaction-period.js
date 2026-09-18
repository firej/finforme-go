(function(root) {
  'use strict';
  function monthKey(date) { return date.getFullYear() + '-' + String(date.getMonth() + 1).padStart(2, '0'); }
  function start(month) {
    var parts = month.split('-').map(Number);
    return new Date(parts[0], parts[1] - 1, 1);
  }
  function shiftMonth(month, count) {
    var date = start(month);
    date.setMonth(date.getMonth() + count);
    return monthKey(date);
  }
  function bounds(period, month) {
    if (period === 'all') return {from: '', to: '', label: '', hint: 'За всё время'};
    var date = start(month), length = 1;
    if (period === 'quarter') { date.setMonth(Math.floor(date.getMonth() / 3) * 3); length = 3; }
    if (period === 'year') { date.setMonth(0); length = 12; }
    var end = new Date(date.getFullYear(), date.getMonth() + length, 1);
    var last = new Date(end.getFullYear(), end.getMonth(), 0);
    var label = date.toLocaleDateString('ru-RU', {month:'long', year:'numeric'}).replace(/\s*г\.$/, '');
    if (period === 'quarter') label = (Math.floor(date.getMonth()/3) + 1) + ' квартал ' + date.getFullYear();
    if (period === 'year') label = String(date.getFullYear());
    return {from: monthKey(date) + '-01', to: monthKey(end) + '-01', label: label,
      hint: date.toLocaleDateString('ru-RU') + ' — ' + last.toLocaleDateString('ru-RU')};
  }
  root.TransactionPeriod = {monthKey:monthKey, shiftMonth:shiftMonth, bounds:bounds};
  if (typeof module !== 'undefined' && module.exports) module.exports = root.TransactionPeriod;
})(typeof window !== 'undefined' ? window : globalThis);
