-- Read-only diagnostic queries. Run against a backup or in a read-only session.
-- Cross-currency transactions are intentionally excluded from the balance check.
SELECT COUNT(*) AS transactions_with_fewer_than_two_splits FROM (
 SELECT t.id FROM transactions t LEFT JOIN splits s ON s.tx_id=t.id
 GROUP BY t.id HAVING COUNT(s.id)<2
) anomalies;

SELECT COUNT(*) AS broken_split_references FROM splits s
LEFT JOIN transactions t ON t.id=s.tx_id
LEFT JOIN accounts a ON a.id=s.account_id
WHERE t.id IS NULL OR a.id IS NULL OR s.user_id<>t.user_id OR s.user_id<>a.user_id;

SELECT COUNT(*) AS invalid_split_denominators FROM splits WHERE value_denom IS NULL OR value_denom<>100;

SELECT COUNT(*) AS unbalanced_single_currency_transactions FROM (
 SELECT s.tx_id FROM splits s JOIN accounts a ON a.id=s.account_id
 JOIN commodities c ON c.id=a.commodity_id
 GROUP BY s.tx_id HAVING COUNT(DISTINCT c.mnemonic)=1
 AND MIN(s.value_denom)=100 AND MAX(s.value_denom)=100 AND SUM(s.value_num)<>0
) anomalies;

SELECT COUNT(*) AS accounts_without_currency FROM accounts a
LEFT JOIN commodities c ON c.id=a.commodity_id
WHERE a.account_type<>'ROOT' AND c.id IS NULL;
