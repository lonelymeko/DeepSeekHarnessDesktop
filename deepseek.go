package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Endpoints the settings panel reads. Balance is a documented public endpoint;
// the usage pair is the platform's own web API, which is why it needs a login
// token rather than the model API key.
const (
	deepSeekPublicBaseURL   = "https://api.deepseek.com"
	deepSeekPlatformBaseURL = "https://platform.deepseek.com"
	deepSeekBalancePath     = "/user/balance"
	deepSeekUsageAmountPath = "/api/v0/usage/amount"
	deepSeekUsageCostPath   = "/api/v0/usage/cost"
	deepSeekUsageReferer    = "https://platform.deepseek.com/usage"
)

// Credential references the panel resolves through the Harness credential seam.
const (
	deepSeekAPIKeyRef    = "DEEPSEEK_API_KEY"
	deepSeekUserTokenRef = "DEEPSEEK_USER_TOKEN"
)

// deepSeekBrowserUserAgent is the user agent the platform's web API expects;
// it rejects the desktop shell's own identifier.
const deepSeekBrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"

// deepSeekMaxResponseBytes bounds every response the panel parses.
const deepSeekMaxResponseBytes = 1 << 20

// deepSeekUsageTypes are the only token rows the platform reports; every other
// type counts toward the total without a breakdown line.
const (
	deepSeekRequestType     = "REQUEST"
	deepSeekCacheHitType    = "PROMPT_CACHE_HIT_TOKEN"
	deepSeekCacheMissType   = "PROMPT_CACHE_MISS_TOKEN"
	deepSeekResponseType    = "RESPONSE_TOKEN"
	deepSeekDefaultCurrency = "CNY"
)

// DeepSeekBalanceInfo is one currency's remaining balance.
type DeepSeekBalanceInfo struct {
	Currency        string `json:"currency"`
	TotalBalance    string `json:"totalBalance"`
	GrantedBalance  string `json:"grantedBalance"`
	ToppedUpBalance string `json:"toppedUpBalance"`
}

// DeepSeekBalance is the account's quota, from the public balance endpoint.
type DeepSeekBalance struct {
	Available bool                  `json:"available"`
	Infos     []DeepSeekBalanceInfo `json:"infos"`
}

// DeepSeekUsageWindow is one time window's totals.
type DeepSeekUsageWindow struct {
	Tokens   int64   `json:"tokens"`
	Requests int64   `json:"requests"`
	Cost     float64 `json:"cost"`
}

// DeepSeekUsageBreakdown splits the month's tokens by cache outcome.
type DeepSeekUsageBreakdown struct {
	CacheHit  int64 `json:"cacheHit"`
	CacheMiss int64 `json:"cacheMiss"`
	Output    int64 `json:"output"`
}

// DeepSeekModelUsage is one model's share of the month.
type DeepSeekModelUsage struct {
	Model  string  `json:"model"`
	Tokens int64   `json:"tokens"`
	Cost   float64 `json:"cost"`
}

// DeepSeekUsage is the account's consumption for the current month.
type DeepSeekUsage struct {
	Currency  string                 `json:"currency"`
	Month     DeepSeekUsageWindow    `json:"month"`
	Today     DeepSeekUsageWindow    `json:"today"`
	Breakdown DeepSeekUsageBreakdown `json:"breakdown"`
	Models    []DeepSeekModelUsage   `json:"models"`
}

// DeepSeekOverview is everything the desktop settings panel renders. The two
// halves fail independently: a valid API key with no platform login token
// still shows the balance, and reports why usage is missing.
type DeepSeekOverview struct {
	APIKeyConfigured    bool             `json:"apiKeyConfigured"`
	APIKeySource        string           `json:"apiKeySource"`
	UserTokenConfigured bool             `json:"userTokenConfigured"`
	Balance             *DeepSeekBalance `json:"balance,omitempty"`
	Usage               *DeepSeekUsage   `json:"usage,omitempty"`
	BalanceError        string           `json:"balanceError,omitempty"`
	UsageError          string           `json:"usageError,omitempty"`
	ProxySource         string           `json:"proxySource,omitempty"`
	ProxyRouted         bool             `json:"proxyRouted"`
	FetchedAt           string           `json:"fetchedAt"`
}

// GetDeepSeekOverview resolves the configured DeepSeek credentials and reports
// the account's balance and this month's usage. It never returns an error for a
// remote failure: the panel shows which half failed and why, so one unreachable
// endpoint does not blank the whole section.
func (a *App) GetDeepSeekOverview() (DeepSeekOverview, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	plan := a.desktopPlan()
	overview := DeepSeekOverview{
		FetchedAt:   time.Now().UTC().Format(time.RFC3339),
		ProxyRouted: plan.routed(),
		ProxySource: plan.Source,
	}
	home := a.harnessHome
	if home == "" {
		if resolved, err := sharedHarnessHome(); err == nil {
			home = resolved
		}
	}
	client, closeIdleConnections := a.deepSeekClient()
	defer closeIdleConnections()

	if apiKey, source, found := harnessCredential(home, deepSeekAPIKeyRef, osGetenv); found {
		overview.APIKeyConfigured = true
		overview.APIKeySource = source
		balance, err := fetchDeepSeekBalance(ctx, client, deepSeekPublicBaseURL, apiKey)
		if err != nil {
			overview.BalanceError = err.Error()
		} else {
			overview.Balance = &balance
		}
	}

	if token, _, found := harnessCredential(home, deepSeekUserTokenRef, osGetenv); found {
		overview.UserTokenConfigured = true
		usage, err := fetchDeepSeekMonthlyUsage(ctx, client, deepSeekPlatformBaseURL, token, time.Now())
		if err != nil {
			overview.UsageError = err.Error()
		} else {
			overview.Usage = &usage
		}
	}
	return overview, nil
}

// fetchDeepSeekBalance reads the public balance endpoint.
func fetchDeepSeekBalance(ctx context.Context, client *http.Client, baseURL, apiKey string) (DeepSeekBalance, error) {
	var payload struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency        string `json:"currency"`
			TotalBalance    string `json:"total_balance"`
			GrantedBalance  string `json:"granted_balance"`
			ToppedUpBalance string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	endpoint := strings.TrimRight(baseURL, "/") + deepSeekBalancePath
	if err := deepSeekGetJSON(ctx, client, endpoint, map[string]string{
		"Authorization": "Bearer " + apiKey,
	}, &payload); err != nil {
		return DeepSeekBalance{}, err
	}
	balance := DeepSeekBalance{Available: payload.IsAvailable, Infos: []DeepSeekBalanceInfo{}}
	for _, info := range payload.BalanceInfos {
		balance.Infos = append(balance.Infos, DeepSeekBalanceInfo{
			Currency:        info.Currency,
			TotalBalance:    info.TotalBalance,
			GrantedBalance:  info.GrantedBalance,
			ToppedUpBalance: info.ToppedUpBalance,
		})
	}
	return balance, nil
}

// fetchDeepSeekMonthlyUsage reads the platform's amount and cost reports for
// the month `now` falls in.
func fetchDeepSeekMonthlyUsage(ctx context.Context, client *http.Client, baseURL, userToken string, now time.Time) (DeepSeekUsage, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + userToken,
		"User-Agent":    deepSeekBrowserUserAgent,
		"Referer":       deepSeekUsageReferer,
	}
	query := fmt.Sprintf("?month=%d&year=%d", int(now.Month()), now.Year())
	var amount deepSeekAmountEnvelope
	if err := deepSeekGetJSON(ctx, client, strings.TrimRight(baseURL, "/")+deepSeekUsageAmountPath+query, headers, &amount); err != nil {
		return DeepSeekUsage{}, err
	}
	var cost deepSeekCostEnvelope
	if err := deepSeekGetJSON(ctx, client, strings.TrimRight(baseURL, "/")+deepSeekUsageCostPath+query, headers, &cost); err != nil {
		return DeepSeekUsage{}, err
	}
	return summarizeDeepSeekUsage(amount, cost, now), nil
}

// deepSeekGetJSON performs one authenticated GET and decodes its JSON body.
func deepSeekGetJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "DeepSeekHarnessDesktop/"+desktopVersion)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, deepSeekMaxResponseBytes))
	if err != nil {
		return fmt.Errorf("read %s: %w", endpoint, err)
	}
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("credential rejected by %s (HTTP %d)", endpoint, response.StatusCode)
	default:
		return fmt.Errorf("%s returned HTTP %d: %s", endpoint, response.StatusCode, truncateText(string(body), 200))
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return nil
}

// deepSeekAmountEnvelope is the `usage/amount` response: `biz_data` is an
// object holding the month's per-model rows plus its per-day split.
type deepSeekAmountEnvelope struct {
	Data struct {
		BizData struct {
			Total []deepSeekUsageModel `json:"total"`
			Days  []deepSeekUsageDay   `json:"days"`
		} `json:"biz_data"`
	} `json:"data"`
}

// deepSeekCostEnvelope is the `usage/cost` response, whose `biz_data` is an
// array of per-currency reports rather than an object.
type deepSeekCostEnvelope struct {
	Data struct {
		BizData []deepSeekCostReport `json:"biz_data"`
	} `json:"data"`
}

// deepSeekCostReport is one currency's report.
type deepSeekCostReport struct {
	Currency string               `json:"currency"`
	Total    []deepSeekUsageModel `json:"total"`
	Days     []deepSeekUsageDay   `json:"days"`
}

// deepSeekUsageModel groups one model's rows.
type deepSeekUsageModel struct {
	Model string               `json:"model"`
	Usage []deepSeekUsageEntry `json:"usage"`
}

// deepSeekUsageDay groups one day's rows.
type deepSeekUsageDay struct {
	Date string               `json:"date"`
	Data []deepSeekUsageModel `json:"data"`
}

// deepSeekUsageEntry is one `{type, amount}` row. In the amount report the
// amount is a token count; in the cost report it is money.
type deepSeekUsageEntry struct {
	Type   string         `json:"type"`
	Amount deepSeekNumber `json:"amount"`
}

// deepSeekNumber admits the platform's numeric fields, which arrive as either
// a JSON number or a numeric string depending on the endpoint and the account.
// An unparseable value reads as zero rather than failing the whole panel.
type deepSeekNumber float64

// UnmarshalJSON implements json.Unmarshaler.
func (n *deepSeekNumber) UnmarshalJSON(data []byte) error {
	text := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if text == "" || text == "null" {
		*n = 0
		return nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		*n = 0
		return nil
	}
	*n = deepSeekNumber(value)
	return nil
}

// summarizeDeepSeekUsage folds both reports into one monthly summary, matching
// on the UTC calendar day the platform buckets its daily rows by.
func summarizeDeepSeekUsage(amount deepSeekAmountEnvelope, cost deepSeekCostEnvelope, now time.Time) DeepSeekUsage {
	usage := DeepSeekUsage{Currency: deepSeekDefaultCurrency, Models: []DeepSeekModelUsage{}}
	report := deepSeekCostReport{}
	if len(cost.Data.BizData) > 0 {
		report = cost.Data.BizData[0]
	}
	if report.Currency != "" {
		usage.Currency = report.Currency
	}

	modelTokens := map[string]int64{}
	modelCost := map[string]float64{}
	order := []string{}
	for _, model := range amount.Data.BizData.Total {
		var subtotal int64
		for _, entry := range model.Usage {
			value := int64(entry.Amount)
			if entry.Type == deepSeekRequestType {
				usage.Month.Requests += value
				continue
			}
			subtotal += value
			usage.Month.Tokens += value
			switch entry.Type {
			case deepSeekCacheHitType:
				usage.Breakdown.CacheHit += value
			case deepSeekCacheMissType:
				usage.Breakdown.CacheMiss += value
			case deepSeekResponseType:
				usage.Breakdown.Output += value
			}
		}
		if model.Model == "" {
			continue
		}
		if _, seen := modelTokens[model.Model]; !seen {
			order = append(order, model.Model)
		}
		modelTokens[model.Model] += subtotal
	}

	for _, model := range report.Total {
		var subtotal float64
		for _, entry := range model.Usage {
			// The cost report repeats the request count as a non-monetary row.
			if entry.Type == deepSeekRequestType {
				continue
			}
			subtotal += float64(entry.Amount)
		}
		usage.Month.Cost += subtotal
		if model.Model != "" {
			modelCost[model.Model] += subtotal
		}
	}

	today := now.UTC().Format("2006-01-02")
	for _, day := range amount.Data.BizData.Days {
		if normalizeDeepSeekDate(day.Date) != today {
			continue
		}
		for _, model := range day.Data {
			for _, entry := range model.Usage {
				value := int64(entry.Amount)
				if entry.Type == deepSeekRequestType {
					usage.Today.Requests += value
					continue
				}
				usage.Today.Tokens += value
			}
		}
	}
	for _, day := range report.Days {
		if normalizeDeepSeekDate(day.Date) != today {
			continue
		}
		for _, model := range day.Data {
			for _, entry := range model.Usage {
				if entry.Type == deepSeekRequestType {
					continue
				}
				usage.Today.Cost += float64(entry.Amount)
			}
		}
	}

	for _, model := range order {
		usage.Models = append(usage.Models, DeepSeekModelUsage{
			Model:  model,
			Tokens: modelTokens[model],
			Cost:   modelCost[model],
		})
	}
	sort.SliceStable(usage.Models, func(left, right int) bool {
		return usage.Models[left].Tokens > usage.Models[right].Tokens
	})
	return usage
}

// normalizeDeepSeekDate admits both `2026-09-01` and the compact `20260901`
// form the export endpoint uses.
func normalizeDeepSeekDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 8 && !strings.Contains(value, "-") {
		return value[0:4] + "-" + value[4:6] + "-" + value[6:8]
	}
	return value
}

// truncateText shortens a response body for an error message.
func truncateText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
