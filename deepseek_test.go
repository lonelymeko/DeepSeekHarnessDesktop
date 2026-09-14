package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// amountFixture mirrors the platform's `usage/amount` response, including the
// numeric-string form one endpoint sends alongside plain numbers.
const amountFixture = `{
  "data": {
    "biz_data": {
      "total": [
        {"model": "deepseek-chat", "usage": [
          {"type": "REQUEST", "amount": 12},
          {"type": "PROMPT_CACHE_HIT_TOKEN", "amount": 100},
          {"type": "PROMPT_CACHE_MISS_TOKEN", "amount": 200},
          {"type": "RESPONSE_TOKEN", "amount": 50}
        ]},
        {"model": "deepseek-reasoner", "usage": [
          {"type": "REQUEST", "amount": 3},
          {"type": "PROMPT_CACHE_MISS_TOKEN", "amount": "1000"},
          {"type": "RESPONSE_TOKEN", "amount": "500"}
        ]}
      ],
      "days": [
        {"date": "20260910", "data": [
          {"model": "deepseek-chat", "usage": [
            {"type": "REQUEST", "amount": 2},
            {"type": "PROMPT_CACHE_MISS_TOKEN", "amount": 10},
            {"type": "RESPONSE_TOKEN", "amount": 5}
          ]}
        ]},
        {"date": "2026-09-09", "data": [
          {"model": "deepseek-chat", "usage": [
            {"type": "RESPONSE_TOKEN", "amount": 999}
          ]}
        ]}
      ]
    }
  }
}`

// costFixture mirrors `usage/cost`, whose `biz_data` is an array of reports.
const costFixture = `{
  "data": {
    "biz_data": [
      {
        "currency": "CNY",
        "total": [
          {"model": "deepseek-chat", "usage": [
            {"type": "REQUEST", "amount": 12},
            {"type": "PROMPT_CACHE_HIT_TOKEN", "amount": 0.01},
            {"type": "PROMPT_CACHE_MISS_TOKEN", "amount": 0.20},
            {"type": "RESPONSE_TOKEN", "amount": 0.05}
          ]},
          {"model": "deepseek-reasoner", "usage": [
            {"type": "REQUEST", "amount": 3},
            {"type": "PROMPT_CACHE_MISS_TOKEN", "amount": "1.00"},
            {"type": "RESPONSE_TOKEN", "amount": "0.50"}
          ]}
        ],
        "days": [
          {"date": "20260910", "data": [
            {"model": "deepseek-chat", "usage": [
              {"type": "REQUEST", "amount": 2},
              {"type": "PROMPT_CACHE_MISS_TOKEN", "amount": 0.02},
              {"type": "RESPONSE_TOKEN", "amount": 0.01}
            ]}
          ]}
        ]
      }
    ]
  }
}`

func decodeAmountFixture(t *testing.T) deepSeekAmountEnvelope {
	t.Helper()
	var amount deepSeekAmountEnvelope
	if err := json.Unmarshal([]byte(amountFixture), &amount); err != nil {
		t.Fatal(err)
	}
	return amount
}

func decodeCostFixture(t *testing.T) deepSeekCostEnvelope {
	t.Helper()
	var cost deepSeekCostEnvelope
	if err := json.Unmarshal([]byte(costFixture), &cost); err != nil {
		t.Fatal(err)
	}
	return cost
}

func TestSummarizeDeepSeekUsage(t *testing.T) {
	// The platform buckets its daily rows by UTC, so the window is matched there.
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	usage := summarizeDeepSeekUsage(decodeAmountFixture(t), decodeCostFixture(t), now)

	if usage.Currency != "CNY" {
		t.Errorf("Currency = %q", usage.Currency)
	}
	// 100+200+50 from deepseek-chat, 1000+500 from deepseek-reasoner.
	if usage.Month.Tokens != 1850 {
		t.Errorf("Month.Tokens = %d, want 1850", usage.Month.Tokens)
	}
	if usage.Month.Requests != 15 {
		t.Errorf("Month.Requests = %d, want 15", usage.Month.Requests)
	}
	if closeEnough(usage.Month.Cost, 1.76) == false {
		t.Errorf("Month.Cost = %v, want 1.76", usage.Month.Cost)
	}
	if usage.Breakdown.CacheHit != 100 || usage.Breakdown.CacheMiss != 1200 || usage.Breakdown.Output != 550 {
		t.Errorf("Breakdown = %+v", usage.Breakdown)
	}
	if usage.Today.Tokens != 15 || usage.Today.Requests != 2 {
		t.Errorf("Today = %+v, want 15 tokens over 2 requests", usage.Today)
	}
	if !closeEnough(usage.Today.Cost, 0.03) {
		t.Errorf("Today.Cost = %v, want 0.03", usage.Today.Cost)
	}
	if len(usage.Models) != 2 {
		t.Fatalf("Models = %+v", usage.Models)
	}
	if usage.Models[0].Model != "deepseek-reasoner" || usage.Models[0].Tokens != 1500 {
		t.Errorf("models must be sorted by tokens descending: %+v", usage.Models)
	}
	if usage.Models[1].Model != "deepseek-chat" || usage.Models[1].Tokens != 350 {
		t.Errorf("models = %+v", usage.Models)
	}
	if !closeEnough(usage.Models[1].Cost, 0.26) {
		t.Errorf("deepseek-chat cost = %v, want 0.26", usage.Models[1].Cost)
	}
}

func TestSummarizeDeepSeekUsageToleratesMissingReports(t *testing.T) {
	usage := summarizeDeepSeekUsage(deepSeekAmountEnvelope{}, deepSeekCostEnvelope{}, time.Now())
	if usage.Currency != deepSeekDefaultCurrency {
		t.Errorf("Currency = %q, want the default", usage.Currency)
	}
	if usage.Month.Tokens != 0 || usage.Month.Cost != 0 || len(usage.Models) != 0 {
		t.Errorf("an empty response must summarize to zeroes: %+v", usage)
	}
}

func TestDeepSeekNumberAdmitsNumbersAndStrings(t *testing.T) {
	var payload struct {
		Values []deepSeekNumber `json:"values"`
	}
	if err := json.Unmarshal([]byte(`{"values":[12,"34",null,"",1.5,"oops"]}`), &payload); err != nil {
		t.Fatal(err)
	}
	want := []float64{12, 34, 0, 0, 1.5, 0}
	for index, expected := range want {
		if float64(payload.Values[index]) != expected {
			t.Errorf("Values[%d] = %v, want %v", index, payload.Values[index], expected)
		}
	}
}

func TestNormalizeDeepSeekDate(t *testing.T) {
	cases := map[string]string{
		"20260910":   "2026-09-10",
		"2026-09-10": "2026-09-10",
		"":           "",
	}
	for input, want := range cases {
		if got := normalizeDeepSeekDate(input); got != want {
			t.Errorf("normalizeDeepSeekDate(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFetchDeepSeekBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != deepSeekBalancePath {
			t.Errorf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
          "is_available": true,
          "balance_infos": [
            {"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"},
            {"currency":"USD","total_balance":"5.00","granted_balance":"0.00","topped_up_balance":"5.00"}
          ]
        }`))
	}))
	defer server.Close()

	balance, err := fetchDeepSeekBalance(context.Background(), server.Client(), server.URL, "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	if !balance.Available {
		t.Error("Available = false")
	}
	if len(balance.Infos) != 2 {
		t.Fatalf("Infos = %+v", balance.Infos)
	}
	if balance.Infos[0].Currency != "CNY" || balance.Infos[0].TotalBalance != "110.00" ||
		balance.Infos[0].GrantedBalance != "10.00" || balance.Infos[0].ToppedUpBalance != "100.00" {
		t.Errorf("Infos[0] = %+v", balance.Infos[0])
	}
}

func TestFetchDeepSeekBalanceReportsRejectedCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := fetchDeepSeekBalance(context.Background(), server.Client(), server.URL, "sk-bad")
	if err == nil || !strings.Contains(err.Error(), "credential rejected") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchDeepSeekMonthlyUsage(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path+"?"+request.URL.RawQuery)
		if got := request.Header.Get("Authorization"); got != "Bearer user-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := request.Header.Get("Referer"); got != deepSeekUsageReferer {
			t.Errorf("Referer = %q", got)
		}
		if got := request.Header.Get("User-Agent"); !strings.Contains(got, "Mozilla") {
			t.Errorf("User-Agent = %q, the platform API rejects the desktop identifier", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case deepSeekUsageAmountPath:
			_, _ = writer.Write([]byte(amountFixture))
		case deepSeekUsageCostPath:
			_, _ = writer.Write([]byte(costFixture))
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	usage, err := fetchDeepSeekMonthlyUsage(context.Background(), server.Client(), server.URL, "user-token", now)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Month.Tokens != 1850 {
		t.Errorf("Month.Tokens = %d", usage.Month.Tokens)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v", paths)
	}
	for _, path := range paths {
		if !strings.Contains(path, "month=9") || !strings.Contains(path, "year=2026") {
			t.Errorf("path %q must scope the report to the requested month", path)
		}
	}
}

func TestFetchDeepSeekMonthlyUsageReportsRejectedToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	if _, err := fetchDeepSeekMonthlyUsage(context.Background(), server.Client(), server.URL, "expired", time.Now()); err == nil {
		t.Fatal("an expired login token must be reported")
	}
}

func TestParseCredentialRefs(t *testing.T) {
	document := `version: 1
refs:
  DEEPSEEK_API_KEY: sk-abc
  GROK_API_KEY: "sk-quoted"
  CHATGPT_API_KEY: 'sk-single'
records:
  client-connection/browser-session:
    kind: grant
    payload:
      version: 1
      secret: not-a-ref
`
	refs := parseCredentialRefs(document)
	if refs["DEEPSEEK_API_KEY"] != "sk-abc" {
		t.Errorf("DEEPSEEK_API_KEY = %q", refs["DEEPSEEK_API_KEY"])
	}
	if refs["GROK_API_KEY"] != "sk-quoted" {
		t.Errorf("GROK_API_KEY = %q", refs["GROK_API_KEY"])
	}
	if refs["CHATGPT_API_KEY"] != "sk-single" {
		t.Errorf("CHATGPT_API_KEY = %q", refs["CHATGPT_API_KEY"])
	}
	// `records` is addressed by scope/id, never by a CredentialRef, so nothing
	// nested under it may leak into the reference mapping.
	if len(refs) != 3 {
		t.Fatalf("refs = %v", refs)
	}
}

func TestCredentialFromEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# comment\nDEEPSEEK_API_KEY=from-dotenv\nOTHER=\"quoted\"\nexport EXPORTED=yes\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := credentialFromEnvFile(path, "DEEPSEEK_API_KEY"); got != "from-dotenv" {
		t.Errorf("DEEPSEEK_API_KEY = %q", got)
	}
	if got := credentialFromEnvFile(path, "OTHER"); got != "quoted" {
		t.Errorf("OTHER = %q", got)
	}
	if got := credentialFromEnvFile(path, "EXPORTED"); got != "yes" {
		t.Errorf("EXPORTED = %q", got)
	}
	if got := credentialFromEnvFile(path, "ABSENT"); got != "" {
		t.Errorf("ABSENT = %q", got)
	}
}

func TestHarnessCredentialPrecedence(t *testing.T) {
	home := t.TempDir()
	document := "version: 1\nrefs:\n  DEEPSEEK_API_KEY: from-file\n"
	if err := os.WriteFile(filepath.Join(home, credentialDocumentName), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}

	value, source, found := harnessCredential(home, "DEEPSEEK_API_KEY", func(string) string { return "" })
	if !found || value != "from-file" {
		t.Fatalf("harnessCredential = (%q, %q, %v)", value, source, found)
	}
	if source != credentialDocumentName {
		t.Errorf("source = %q", source)
	}

	// The inherited environment wins, exactly as the upstream seam documents.
	value, source, found = harnessCredential(home, "DEEPSEEK_API_KEY", func(name string) string {
		if name == "DEEPSEEK_API_KEY" {
			return "from-env"
		}
		return ""
	})
	if !found || value != "from-env" {
		t.Fatalf("harnessCredential = (%q, %q, %v)", value, source, found)
	}
	if !strings.Contains(source, "环境变量") {
		t.Errorf("source = %q", source)
	}

	// An unconfigured reference must report absence rather than an empty value.
	if _, _, found := harnessCredential(home, "NOPE_API_KEY", func(string) string { return "" }); found {
		t.Error("a reference that is stored nowhere must report absence")
	}
}

func closeEnough(left, right float64) bool {
	difference := left - right
	if difference < 0 {
		difference = -difference
	}
	return difference < 1e-9
}
