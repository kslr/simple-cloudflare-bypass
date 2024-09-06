# Simple Cloudflare Bypass

This service helps you bypass Cloudflare’s anti-bot protection by leveraging 2captcha for solving challenges. Once you send a request to the service, it get the necessary cf_clearance cookie, allowing you to access Cloudflare-protected websites. To ensure success, you need to configure a proxy on your server so that 2captcha workers use the same IP address as your service, as the cf_clearance is valid only for the same IP and User-Agent.

## Usage

### Install Chrome
see https://go-rod.github.io/#/compatibility?id=os


### Run the service
```bash
Usage of ./app:
  -api-key string
        API key for accessing this service
  -listen-address string
        HTTP server listen address (default "0.0.0.0:2316")
  -twocaptcha-api-key string
        2captcha api key
  -proxy-dsn string
        Proxy dsn, example: http://user:pass@host:port
```

### Send Requests
```bash
POST http://localhost:8080/bypass
Content-Type: application/json
Authorization: Bearer myapikey

{
  "url": "https://www.javlibrary.com",
  "userAgent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:129.0) Gecko/20100101 Firefox/129.0",
  "cookies": [
    "hello": "world"
  ]
}
```
### Response
Failed Response

```bash
HTTP/1.1 400 Bad Request
HTTP/1.1 401 Unauthorized

HTTP/1.1 500
{
    "message": "captcha solving timeout",
}
```

Successful Response
```bash
HTTP/1.1 200 OK

{
  "message": "",
  "elapsed_time": 12, // seconds
  "solution": {
    "url": "https://www.javlibrary.com",
    "status": 200,
    "response": "<html>.....",
    "cookies": [
      {
        "name": "cf_clearance",
        "value": "1234567890abcdef1234567890abcdef1234567890-1234567890-1234567890-1234567890",
        "domain": ".javlibrary.com",
        "path": "/",
        "expires": 1756663333.294741,
        "size": 523,
        "httpOnly": true,
        "secure": true,
        "session": false,
        "sameSite": "None",
        "priority": "Medium",
        "sameParty": false,
        "sourceScheme": "Secure",
        "sourcePort": 443,
        "partitionKey": {
          "topLevelSite": "https://javlibrary.com",
          "hasCrossSiteAncestor": false
      }
      ...
    },
  }
}
```

## Referral
[2captcha 1000 solve for $1.45](https://2captcha.com?from=12593669) ☕️