# Simple Cloudflare Bypass

This service helps you bypass Cloudflare’s anti-bot protection by leveraging 2captcha for solving challenges. Once you send a request to the service, it get the necessary cf_clearance cookie, allowing you to access Cloudflare-protected websites. To ensure success, you need to configure a proxy on your server so that 2captcha workers use the same IP address as your service, as the cf_clearance is valid only for the same IP and User-Agent.

## Usage

### Run the service
```
Usage of ./app:
  -api-key string
        API key for accessing this service
  -listen-address string
        HTTP server listen address (default "0.0.0.0:2316")
  -twocaptcha-api-key string
        2captcha api key
  -twocaptcha-proxy-dsn string
        2captcha proxy dsn, example: http://user:pass@host:port
```

### Send Requests
```
POST http://localhost:8080/bypass
Content-Type: application/json
Authorization: Bearer myapikey

{
  "url": "https://www.javlibrary.com",
  "userAgent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:129.0) Gecko/20100101 Firefox/129.0",
}
```
### Response
Failed Response

```
HTTP/1.1 400 Bad Request
HTTP/1.1 401 Unauthorized

HTTP/1.1 500
{
    "message": "error message"
}
```

Successful Response
```
HTTP/1.1 200 OK

{
  "status": 200, // request page response status code
  "response": "<html>.....", // request page content
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
    },
    ...
  ]
}
```

## Referral
[2captcha 1000 solve for $1.45](https://2captcha.com?from=12593669) ☕️