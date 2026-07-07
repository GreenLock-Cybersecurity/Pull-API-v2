package controllers

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
)

// DemoCheckoutPage serves the mock payment HTML for demo deployments.
// Reached via MockProcessor.CreateCheckout, which constructs a URL pointing
// here. When the user clicks "Pagar ahora", the page calls
// /api/v1/orders/confirm and then redirects to the merchant's success URL.
func DemoCheckoutPage(c *gin.Context) {
	sessionID := c.Query("session_id")
	venueID := c.Query("venue_id")
	successURL := c.Query("success")
	cancelURL := c.Query("cancel")
	amount := c.Query("amount")
	currency := c.Query("currency")
	product := c.Query("product")

	if sessionID == "" || venueID == "" {
		c.String(http.StatusBadRequest, "missing session_id or venue_id")
		return
	}
	if cancelURL == "" {
		cancelURL = successURL
	}

	confirmURL := fmt.Sprintf("/api/v1/orders/confirm?session_id=%s&venue_id=%s",
		url.QueryEscape(sessionID), url.QueryEscape(venueID))

	html := fmt.Sprintf(demoCheckoutHTML,
		htmlEscape(product),
		htmlEscape(amount),
		htmlEscape(currency),
		htmlEscape(sessionID),
		jsString(confirmURL),
		jsString(successURL),
		jsString(successURL),
		jsString(cancelURL),
	)

	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

// htmlEscape replaces the four HTML-sensitive characters.
func htmlEscape(s string) string {
	replacer := []struct{ old, new string }{
		{"&", "&amp;"},
		{"<", "&lt;"},
		{">", "&gt;"},
		{`"`, "&quot;"},
		{"'", "&#39;"},
	}
	out := s
	for _, r := range replacer {
		out = replaceAll(out, r.old, r.new)
	}
	return out
}

// jsString escapes a string for safe embedding inside a JS single-quoted string.
func jsString(s string) string {
	out := s
	out = replaceAll(out, `\`, `\\`)
	out = replaceAll(out, `'`, `\'`)
	out = replaceAll(out, "\n", `\n`)
	out = replaceAll(out, "\r", `\r`)
	out = replaceAll(out, "<", `\x3c`)
	return out
}

func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	out := ""
	for {
		i := indexOf(s, old)
		if i < 0 {
			out += s
			return out
		}
		out += s[:i] + new
		s = s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	if len(sub) > len(s) {
		return -1
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

const demoCheckoutHTML = `<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Pago demo · Aurora Hall</title>
<style>
  * { box-sizing: border-box; }
  body { background: radial-gradient(circle at top, #1a1a2e 0%%, #0a0a0f 60%%); color: #fff; font-family: -apple-system, BlinkMacSystemFont, system-ui, sans-serif; margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 20px; }
  .card { max-width: 460px; width: 100%%; padding: 36px 32px; background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.08); border-radius: 18px; backdrop-filter: blur(10px); box-shadow: 0 30px 60px rgba(0,0,0,0.4); }
  .badge { display: inline-block; background: rgba(245,158,11,0.15); color: #f59e0b; padding: 5px 12px; border-radius: 999px; font-size: 11px; font-weight: 600; letter-spacing: 1.2px; margin-bottom: 18px; }
  .brand { font-size: 13px; letter-spacing: 3px; color: #8b5cf6; font-weight: 700; margin-bottom: 6px; }
  h1 { font-size: 28px; margin: 0 0 28px; font-weight: 700; line-height: 1.2; }
  .product { background: linear-gradient(135deg, rgba(99,102,241,0.12), rgba(139,92,246,0.06)); border-left: 3px solid #6366f1; padding: 18px 18px; border-radius: 10px; margin-bottom: 28px; }
  .product-name { font-size: 14px; color: #a0a0b0; margin-bottom: 6px; }
  .product-amount { font-size: 28px; font-weight: 700; letter-spacing: -0.5px; }
  button { width: 100%%; padding: 15px; background: linear-gradient(135deg,#6366f1,#8b5cf6); color: #fff; border: none; border-radius: 12px; font-size: 16px; font-weight: 600; cursor: pointer; transition: transform 0.15s, box-shadow 0.15s; font-family: inherit; }
  button:hover:not(:disabled) { transform: translateY(-1px); box-shadow: 0 8px 24px rgba(99,102,241,0.4); }
  button:disabled { opacity: 0.6; cursor: not-allowed; }
  .secondary { background: transparent; border: 1px solid rgba(255,255,255,0.12); color: #a0a0b0; margin-top: 10px; }
  .secondary:hover:not(:disabled) { background: rgba(255,255,255,0.05); }
  .meta { font-size: 11px; color: #6b6b7b; margin-top: 18px; text-align: center; line-height: 1.5; }
  .processing { text-align: center; }
  .spinner { width: 56px; height: 56px; border: 3px solid rgba(99,102,241,0.15); border-top-color: #6366f1; border-radius: 50%%; margin: 24px auto 18px; animation: spin 0.9s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
</head>
<body>
<div class="card" id="checkout">
  <div class="badge">MODO DEMO</div>
  <div class="brand">AURORA HALL</div>
  <h1>Confirmar pago</h1>
  <div class="product">
    <div class="product-name">%s</div>
    <div class="product-amount">%s %s</div>
  </div>
  <button id="pay-btn">Pagar ahora</button>
  <button class="secondary" id="cancel-btn">Cancelar</button>
  <div class="meta">Demo · No se procesará dinero real<br>Session: %s</div>
</div>
<div class="card" id="processing" style="display:none">
  <div class="processing">
    <div class="spinner"></div>
    <h1 style="text-align:center; margin: 0 0 8px; font-size: 22px">Procesando pago...</h1>
    <p style="text-align:center; color: #a0a0b0; margin: 0; font-size: 14px">Simulando confirmación con la pasarela</p>
  </div>
</div>
<script>
(function() {
  var payBtn = document.getElementById('pay-btn');
  var cancelBtn = document.getElementById('cancel-btn');
  var checkoutEl = document.getElementById('checkout');
  var processingEl = document.getElementById('processing');

  payBtn.addEventListener('click', function() {
    payBtn.disabled = true;
    cancelBtn.disabled = true;
    checkoutEl.style.display = 'none';
    processingEl.style.display = 'block';

    setTimeout(function() {
      fetch('%s', { method: 'GET', credentials: 'omit' })
        .then(function(r) { return r.json().catch(function(){return{};}); })
        .then(function() { window.location.href = '%s'; })
        .catch(function() { window.location.href = '%s'; });
    }, 1200);
  });

  cancelBtn.addEventListener('click', function() {
    window.location.href = '%s';
  });
})();
</script>
</body>
</html>`
