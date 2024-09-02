package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/tidwall/gjson"
)

type QueryRequest struct {
	URL       string `json:"url"`
	UserAgent string `json:"userAgent"`
}

type QueryResponse struct {
	Message  string                 `json:"message"`
	Status   int                    `json:"status"`
	Response string                 `json:"response"`
	Cookies  []*proto.NetworkCookie `json:"cookies"`
}

const jsCode = `() => {
const i = setInterval(() => {
    if (window.turnstile) {
        clearInterval(i)
        window.turnstile.render = (a, b) => {
            let params = {
                sitekey: b.sitekey,
                pageurl: window.location.href,
                data: b.cData,
                pagedata: b.chlPageData,
                action: b.action,
				json: 1
            }
            console.log('intercepted-params:' + JSON.stringify(params))
            window.cfCallback = b.callback
        }
    }
}, 10)
}
`

var (
	listenAddress    string
	apiKey           string
	twoCaptchaApiKey string
	proxyDsn         string
	proxyParsed      *url.URL
)

func main() {
	flag.StringVar(&listenAddress, "listen-address", "0.0.0.0:2316", "HTTP server listen address")
	flag.StringVar(&apiKey, "api-key", "", "API key for accessing this service")
	flag.StringVar(&proxyDsn, "proxy-dsn", "", "Proxy dsn, example: http://user:pass@host:port")
	flag.StringVar(&twoCaptchaApiKey, "twocaptcha-api-key", "", "2captcha api key")
	flag.Parse()

	if apiKey == "" {
		log.Fatal("API key is required")
	}
	if twoCaptchaApiKey == "" {
		log.Fatal("2captcha api key is required")
	}
	if proxyDsn == "" {
		log.Fatal("proxy dsn is required")
	} else {
		proxyParse, err := url.Parse(proxyDsn)
		if err != nil {
			log.Fatalf("invalid proxy dsn: %s", err)
		}
		proxyParsed = proxyParse
	}

	http.HandleFunc("/bypass", handleQuery)
	log.Infof("Listening on %s", listenAddress)
	log.Fatal(http.ListenAndServe(listenAddress, nil))
}

func TouchResponse(w http.ResponseWriter, code int, response *QueryResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+apiKey {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		TouchResponse(w, http.StatusBadRequest, &QueryResponse{
			Message: "Invalid request body",
		})
		return
	}

	logger := log.NewWithOptions(os.Stdout, log.Options{
		ReportCaller:    true,
		ReportTimestamp: true,
		Prefix:          "🕷️ " + req.URL,
	})

	u := launcher.New().
		UserDataDir("/tmp/chrome-data").
		NoSandbox(true).
		Headless(false).
		Set("user-agent", req.UserAgent).
		Set("no-sandbox").
		Set("window-size", "1920,1080").
		Set("disable-setuid-sandbox").
		Set("disable-dev-shm-usage").
		Set("no-first-run").
		Set("disable-blink-features", "AutomationControlled").
		MustLaunch()
	browser := rod.New().ControlURL(u).MustConnect()
	defer browser.MustClose()

	page := browser.MustPage("")

	var statusCode int
	var interceptedParams string
	interceptedDone := make(chan struct{})
	go page.EachEvent(func(e *proto.RuntimeConsoleAPICalled) bool {
		for _, arg := range e.Args {
			value := arg.Value.String()
			if strings.Contains(value, "intercepted-params:") {
				interceptedParams = strings.TrimPrefix(value, "intercepted-params:")
				close(interceptedDone)
				return true
			}
		}
		return false
	}, func(e *proto.NetworkResponseReceived) {
		statusCode = e.Response.Status
	})()

	page.MustNavigate(req.URL)
	page.MustEval(jsCode)

	title := page.MustInfo().Title
	if title == "Just a moment..." {
		logger.Info("met cloudflare turnstile")

		select {
		case <-interceptedDone:
			logger.Info("start solving captcha")

			proxyPassword, _ := proxyParsed.User.Password()
			solveResult, err := SolveCaptcha(&TurnstileTask{
				Type:          "TurnstileTask",
				WebsiteURL:    req.URL,
				WebsiteKey:    gjson.Get(interceptedParams, "sitekey").String(),
				UserAgent:     req.UserAgent,
				Action:        gjson.Get(interceptedParams, "action").String(),
				Data:          gjson.Get(interceptedParams, "data").String(),
				PageData:      gjson.Get(interceptedParams, "pagedata").String(),
				ProxyType:     proxyParsed.Scheme,
				ProxyAddress:  proxyParsed.Hostname(),
				ProxyPort:     proxyParsed.Port(),
				ProxyLogin:    proxyParsed.User.Username(),
				ProxyPassword: proxyPassword,
			})
			if err != nil {
				TouchResponse(w, http.StatusInternalServerError, &QueryResponse{
					Message: err.Error(),
				})
				return
			}
			logger.Infof("captcha solved, cost: %s, usedTime(s): %d, solveCount: %d", solveResult.Cost, solveResult.EndTime-solveResult.CreateTime, solveResult.SolveCount)

			redirectWait := page.MustWaitNavigation()
			page.MustEval(`() => window.cfCallback("` + solveResult.Solution.Token + `")`)
			redirectWait()
		case <-time.After(30 * time.Second):
			logger.Warn("captcha solving timeout")
			TouchResponse(w, http.StatusInternalServerError, &QueryResponse{Message: "captcha solving timeout"})
			return
		}
	}

	cookies := page.MustCookies(req.URL)
	responseBody := page.MustHTML()

	resp := QueryResponse{
		Status:   statusCode,
		Response: responseBody,
		Cookies:  cookies,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
