package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	api2captcha "github.com/2captcha/2captcha-go"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/pkg/errors"
	"github.com/tidwall/gjson"
)

type QueryRequest struct {
	URL       string `json:"url"`
	UserAgent string `json:"userAgent"`
}

type QueryResponse struct {
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
	listenAddress      string
	apiKey             string
	captchaClient      *api2captcha.Client
	twocaptchaApiKey   string
	twocaptchaProxyDsn string
)

func main() {
	flag.StringVar(&listenAddress, "listen-address", "0.0.0.0:2316", "HTTP server listen address")
	flag.StringVar(&apiKey, "api-key", "", "API key for accessing this service")
	flag.StringVar(&twocaptchaApiKey, "twocaptcha-api-key", "", "2captcha api key")
	flag.StringVar(&twocaptchaProxyDsn, "twocaptcha-proxy-dsn", "", "2captcha proxy dsn, example: http://user:pass@host:port")
	flag.Parse()

	if twocaptchaApiKey == "" {
		log.Fatal("2captcha api key is required")
	}
	captchaClient = api2captcha.NewClient(twocaptchaApiKey)

	if apiKey == "" {
		log.Fatal("API key is required")
	}

	http.HandleFunc("/bypass", handleQuery)
	log.Printf("Starting server on %s", listenAddress)
	log.Fatal(http.ListenAndServe(listenAddress, nil))
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
		writeFailResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	u := launcher.New().
		UserDataDir("/tmp/chrome-data").
		NoSandbox(true).
		Headless(true).
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
		select {
		case <-interceptedDone:
			log.Printf("interceptedParams: %s in %s", interceptedParams, req.URL)
			captcha := api2captcha.CloudflareTurnstile{
				SiteKey:   gjson.Get(interceptedParams, "sitekey").String(),
				Url:       gjson.Get(interceptedParams, "pageurl").String(),
				Data:      gjson.Get(interceptedParams, "data").String(),
				PageData:  gjson.Get(interceptedParams, "pagedata").String(),
				Action:    gjson.Get(interceptedParams, "action").String(),
				UserAgent: req.UserAgent,
			}
			captchaReq := captcha.ToRequest()
			u, err := url.Parse(twocaptchaProxyDsn)
			if err != nil {
				writeFailResponse(w, http.StatusInternalServerError, "invalid proxy dsn")
				return
			}
			captchaReq.SetProxy(u.Scheme, strings.TrimPrefix(twocaptchaProxyDsn, u.Scheme+"://"))
			token, _, err := captchaClient.Solve(captcha.ToRequest())
			if err != nil {
				writeFailResponse(w, http.StatusInternalServerError, errors.Wrapf(err, "2captcha solve failed").Error())
				return
			}
			redirectWait := page.MustWaitNavigation()
			log.Printf("2captcha resolved token: %s", token)
			page.MustEval(`() => window.cfCallback("` + token + `")`)
			redirectWait()
		case <-time.After(30 * time.Second):
			writeFailResponse(w, http.StatusInternalServerError, "wait turnstile timeout")
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

type FailResponse struct {
	Message string `json:"message"`
}

func writeFailResponse(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(FailResponse{Message: message}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
