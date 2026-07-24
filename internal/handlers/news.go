package handlers

import "html/template"

// NewsItem — запись в блоке новостей на главной странице.
type NewsItem struct {
	Date  string // дата в формате DD.MM.YYYY
	Title string
	Body  template.HTML
}

// newsItems — новости проекта, новые сверху.
// Добавление записи: дописать элемент в начало слайса.
var newsItems = []NewsItem{
	{
		Date:  "24.07.2026",
		Title: "MCP-сервер: подключите Claude к своим финансам",
		Body: template.HTML(`
<p>Теперь с вашими данными можно работать из Claude: спрашивать балансы,
добавлять расходы текстом, строить отчёты по категориям. Как подключить:</p>
<ol>
  <li>Создайте API-токен в разделе <a href="/finance/settings">Настройки → API-токены</a></li>
  <li>Подключите MCP-сервер в Claude Code:
    <code class="news-code">claude mcp add --transport http finforme https://finfor.me/mcp --header "Authorization: Bearer ВАШ_ТОКЕН"</code>
    Или в Claude Desktop: Settings → Connectors → Add custom connector,
    URL <code>https://finfor.me/mcp</code> с тем же заголовком.
  </li>
  <li>Попросите: <em>«покажи мои счета в finforme»</em> или
    <em>«добавь расход 500 ₽ на продукты»</em></li>
</ol>
<p>Claude видит только данные владельца токена. Отозвать доступ можно в настройках
в любой момент. Там же работает и обычный REST API (<code>/api/v1</code>).</p>`),
	},
}

// latestNews возвращает новости для главной страницы.
func (h *Handler) latestNews() []NewsItem {
	return newsItems
}
