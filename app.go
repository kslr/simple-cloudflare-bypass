package main

import (
	"flag"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/pkg/errors"
	"github.com/tidwall/gjson"
)

type QueryRequest struct {
	URL       string            `json:"url"`
	UserAgent string            `json:"userAgent"`
	Cookies   map[string]string `json:"cookies"`
}

type QueryResponse struct {
	Message     string           `json:"message"`
	ElapsedTime int              `json:"elapsed_time"`
	Solution    SolutionResponse `json:"solution"`
}

type SolutionResponse struct {
	Url      string                 `json:"url"`
	Status   int                    `json:"status"`
	Response string                 `json:"response"`
	Cookies  []*proto.NetworkCookie `json:"cookies"`
}

var (
	listenAddress    string
	apiKey           string
	twoCaptchaApiKey string
	proxyDsn         string
	proxyParsed      *url.URL
	browser          *rod.Browser
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

	launchUrl := launcher.New().
		UserDataDir("/tmp/rod").
		Headless(false).
		NoSandbox(true).
		Set("window-size", "1920,1080").
		Set("disable-setuid-sandbox").
		Set("disable-dev-shm-usage").
		Set("no-first-run").
		Set("disable-blink-features", "AutomationControlled").
		Set("excludeSwitches", "enable-automation").
		MustLaunch()

	browser = rod.New().ControlURL(launchUrl).MustConnect()
	browser.MustVersion()

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	router.Use(authMiddleware())
	router.GET("/health", healthCheck)
	router.POST("/bypass", handleBypass)
	srv := &http.Server{
		Addr:    listenAddress,
		Handler: router,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		if err := srv.Close(); err != nil {
			log.Fatalf("failed to shutdown server: %s", err)
		}
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("failed to start server: %s", err)
	}

	log.Info("server is shutting down")
}

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth != "Bearer "+apiKey {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "Unauthorized"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func handleBypass(c *gin.Context) {
	var req QueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Invalid request"})
		return
	}
	bypassUrl, err := url.Parse(req.URL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": errors.Wrap(err, "invalid url").Error()})
		return
	}

	startTime := time.Now()
	logger := log.NewWithOptions(os.Stdout, log.Options{
		ReportCaller:    true,
		ReportTimestamp: true,
		Prefix:          "🕷️ " + req.URL,
	})

	page := browser.MustPage("").Context(c)
	defer page.MustClose()

	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: req.UserAgent,
	})

	var cookies []*proto.NetworkCookieParam
	for cookieName, cookieValue := range req.Cookies {
		cookies = append(cookies, &proto.NetworkCookieParam{
			Name:   cookieName,
			Value:  cookieValue,
			Domain: bypassUrl.Hostname(),
		})
	}
	page.MustSetCookies(cookies...)

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
	page.MustEval(`() => {
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
`)

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
				c.JSON(http.StatusInternalServerError, gin.H{"message": errors.Wrap(err, "failed to solve captcha").Error()})
				return
			}
			logger.Infof("captcha solved, cost: %s, usedTime(s): %d, solveCount: %d", solveResult.Cost, solveResult.EndTime-solveResult.CreateTime, solveResult.SolveCount)

			redirectWait := page.MustWaitNavigation()
			page.MustEval(`() => window.cfCallback("` + solveResult.Solution.Token + `")`)
			redirectWait()
		case <-time.After(30 * time.Second):
			logger.Warn("captcha solving timeout")
			c.JSON(http.StatusInternalServerError, gin.H{"message": "captcha solving timeout"})
			return
		}
	}

	resp := QueryResponse{
		Message:     "",
		ElapsedTime: int(time.Since(startTime).Seconds()),
		Solution: SolutionResponse{
			Url:      page.MustInfo().URL,
			Status:   statusCode,
			Response: page.MustHTML(),
			Cookies:  page.MustCookies(req.URL),
		},
	}

	c.PureJSON(http.StatusOK, resp)
}
