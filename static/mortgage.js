// Ипотечный калькулятор — логика расчёта

const STORAGE_KEY = 'mortgage_calc';
let paymentType = 'annuity'; // 'annuity' | 'differentiated'

// ── Утилиты ──────────────────────────────────────────────

function parseNumber(str) {
    if (typeof str === 'number') return str;
    return parseFloat(String(str).replace(/[^\d.,]/g, '').replace(',', '.')) || 0;
}

function formatMoney(value) {
    return Math.round(value).toLocaleString('ru-RU');
}

function formatNumberInput(input) {
    const pos = input.selectionStart;
    const raw = input.value.replace(/[^\d]/g, '');
    if (!raw) { input.value = ''; return; }
    const formatted = parseInt(raw, 10).toLocaleString('ru-RU');
    input.value = formatted;
    // Пытаемся сохранить позицию курсора
    const diff = formatted.length - raw.length;
    const newPos = Math.max(0, pos + diff - (input.value.length - formatted.length));
    requestAnimationFrame(() => input.setSelectionRange(newPos, newPos));
}

// ── Управление UI ────────────────────────────────────────

function setPaymentType(type) {
    paymentType = type;
    const btnA = document.getElementById('btnAnnuity');
    const btnD = document.getElementById('btnDifferentiated');
    if (type === 'annuity') {
        btnA.className = 'flex-1 px-3 py-2 text-sm font-medium bg-primary-600 text-white transition-colors';
        btnD.className = 'flex-1 px-3 py-2 text-sm font-medium bg-white text-gray-700 hover:bg-gray-50 transition-colors';
    } else {
        btnD.className = 'flex-1 px-3 py-2 text-sm font-medium bg-primary-600 text-white transition-colors';
        btnA.className = 'flex-1 px-3 py-2 text-sm font-medium bg-white text-gray-700 hover:bg-gray-50 transition-colors';
    }
    calculate();
}

function updateDownPaymentPercent() {
    const price = parseNumber(document.getElementById('propertyPrice').value);
    const dp = parseNumber(document.getElementById('downPayment').value);
    if (price > 0) {
        const pct = Math.round((dp / price) * 100);
        document.getElementById('downPaymentPercent').textContent = pct + '%';
        document.getElementById('downPaymentRange').value = Math.min(90, Math.max(0, pct));
    }
}

function updateDownPaymentFromRange() {
    const price = parseNumber(document.getElementById('propertyPrice').value);
    const pct = parseInt(document.getElementById('downPaymentRange').value, 10);
    const dp = Math.round(price * pct / 100);
    document.getElementById('downPayment').value = formatMoney(dp);
    document.getElementById('downPaymentPercent').textContent = pct + '%';
}

// ── Расчёт ───────────────────────────────────────────────

function calculate() {
    const price = parseNumber(document.getElementById('propertyPrice').value);
    const downPayment = parseNumber(document.getElementById('downPayment').value);
    const rate = parseNumber(document.getElementById('interestRate').value);
    const years = parseInt(document.getElementById('loanTerm').value, 10) || 0;

    // Синхронизируем ползунок срока
    document.getElementById('loanTermRange').value = years;

    const loan = price - downPayment;
    const months = years * 12;

    if (loan <= 0 || rate <= 0 || months <= 0) {
        clearResults();
        return;
    }

    const monthlyRate = rate / 100 / 12;

    let totalPaymentSum = 0;
    let totalInterestSum = 0;
    let balance = loan;

    if (paymentType === 'annuity') {
        // Аннуитетный платёж: P = S * r * (1+r)^n / ((1+r)^n - 1)
        const pow = Math.pow(1 + monthlyRate, months);
        const monthlyPayment = loan * monthlyRate * pow / (pow - 1);

        for (let i = 1; i <= months; i++) {
            const interestPart = balance * monthlyRate;
            const principalPart = monthlyPayment - interestPart;
            balance -= principalPart;

            totalPaymentSum += monthlyPayment;
            totalInterestSum += interestPart;
        }

        document.getElementById('monthlyPayment').textContent = formatMoney(monthlyPayment) + ' ₽';
    } else {
        // Дифференцированный платёж
        const principalFixed = loan / months;
        let firstPayment = 0;
        let lastPayment = 0;

        for (let i = 1; i <= months; i++) {
            const interestPart = balance * monthlyRate;
            const payment = principalFixed + interestPart;
            balance -= principalFixed;

            totalPaymentSum += payment;
            totalInterestSum += interestPart;

            if (i === 1) firstPayment = payment;
            if (i === months) lastPayment = payment;
        }

        document.getElementById('monthlyPayment').textContent =
            formatMoney(lastPayment) + ' – ' + formatMoney(firstPayment) + ' ₽';
    }

    // Обновляем сводку
    document.getElementById('loanAmount').textContent = formatMoney(loan) + ' ₽';
    document.getElementById('totalInterest').textContent = formatMoney(totalInterestSum) + ' ₽';
    document.getElementById('totalPayment').textContent = formatMoney(totalPaymentSum) + ' ₽';

    // Диаграмма
    const loanPct = (loan / totalPaymentSum) * 100;
    const interestPct = 100 - loanPct;
    document.getElementById('barLoan').style.width = loanPct + '%';
    document.getElementById('barInterest').style.width = interestPct + '%';
    document.getElementById('loanPercent').textContent = Math.round(loanPct) + '%';
    document.getElementById('interestPercent').textContent = Math.round(interestPct) + '%';

    saveToStorage();
}

function clearResults() {
    document.getElementById('monthlyPayment').textContent = '—';
    document.getElementById('loanAmount').textContent = '—';
    document.getElementById('totalInterest').textContent = '—';
    document.getElementById('totalPayment').textContent = '—';
    document.getElementById('barLoan').style.width = '50%';
    document.getElementById('barInterest').style.width = '50%';
    document.getElementById('loanPercent').textContent = '50%';
    document.getElementById('interestPercent').textContent = '50%';
}

// ── localStorage ─────────────────────────────────────────

function saveToStorage() {
    try {
        const data = {
            propertyPrice: document.getElementById('propertyPrice').value,
            downPayment: document.getElementById('downPayment').value,
            interestRate: document.getElementById('interestRate').value,
            loanTerm: document.getElementById('loanTerm').value,
            paymentType: paymentType
        };
        localStorage.setItem(STORAGE_KEY, JSON.stringify(data));
    } catch (e) {
        // localStorage может быть недоступен
    }
}

function loadFromStorage() {
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (!raw) return;
        const data = JSON.parse(raw);

        if (data.propertyPrice) document.getElementById('propertyPrice').value = data.propertyPrice;
        if (data.downPayment) document.getElementById('downPayment').value = data.downPayment;
        if (data.interestRate) document.getElementById('interestRate').value = data.interestRate;
        if (data.loanTerm) document.getElementById('loanTerm').value = data.loanTerm;
        if (data.paymentType) setPaymentType(data.paymentType);

        updateDownPaymentPercent();
    } catch (e) {
        // Если данные повреждены — игнорируем
    }
}

// ── Инициализация ────────────────────────────────────────

document.addEventListener('DOMContentLoaded', function() {
    loadFromStorage();
    calculate();
});
