package demo

import (
	"fmt"
	"html"
	"net/http"
	"strings"
)

// La tienda que recorren los tests de navegador (Playwright, Selenium): login, catálogo,
// producto y carrito con pago. Las páginas llaman a la API de la tienda con fetch, así que la red
// que capturan los clientes es la real. El botón del producto es data-testid="add-to-cart": un
// test escrito contra el selector viejo (#add-to-cart-btn) falla y TraceReports sugiere el nuevo.
// Solo fuentes del sistema: la página no pide nada que no exista (sin errores en la consola).

const storeCSS = `
:root{--bg:#f6f4ef;--surface:#fff;--ink:#17201b;--muted:#5d665f;--line:#e4e0d6;--brand:#1f5f45;--brand-2:#2c7a59;--brand-soft:#e3efe8;--accent:#e8743b;--ok:#1f7a4d;--ok-bg:#e2f4ea;--err:#b3261e;--err-bg:#fbe7e5;--radius:16px}
*{box-sizing:border-box}html{-webkit-text-size-adjust:100%}
body{margin:0;background:var(--bg);color:var(--ink);font:400 16px/1.55 system-ui,-apple-system,"Segoe UI",Roboto,"Helvetica Neue",sans-serif;-webkit-font-smoothing:antialiased}
a{color:inherit}
:focus-visible{outline:3px solid var(--accent);outline-offset:2px;border-radius:8px}
header{background:var(--surface);border-bottom:1px solid var(--line);position:sticky;top:0;z-index:5}
.nav{max-width:1080px;margin:auto;padding:14px 24px;display:flex;align-items:center;gap:28px}
.brand{display:flex;align-items:center;gap:10px;font-weight:800;font-size:19px;letter-spacing:-.02em;text-decoration:none;color:var(--brand)}
.brand-mark{width:30px;height:30px;border-radius:9px;background:var(--brand);display:grid;place-items:center}
.links{display:flex;gap:22px;margin-left:auto;align-items:center}
.links a{text-decoration:none;font-size:15px;font-weight:600;color:var(--muted)}
.links a:hover{color:var(--ink)}
.cart-link{display:inline-flex;align-items:center;gap:7px}
.cart-count{min-width:20px;height:20px;padding:0 6px;border-radius:99px;background:var(--accent);color:#fff;font-size:12px;font-weight:700;display:inline-grid;place-items:center}
main{max-width:1080px;margin:auto;padding:36px 24px 72px}
h1{font-size:clamp(28px,4vw,38px);line-height:1.15;letter-spacing:-.025em;margin:0 0 6px}
.sub{color:var(--muted);margin:0 0 28px}
.card{background:var(--surface);border:1px solid var(--line);border-radius:var(--radius);box-shadow:0 1px 2px rgba(23,32,27,.04),0 8px 28px -18px rgba(23,32,27,.25)}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(270px,1fr));gap:22px}
.product-card{padding:14px 14px 18px;display:flex;flex-direction:column;gap:6px;transition:transform .2s,box-shadow .2s}
.product-card:hover{transform:translateY(-3px);box-shadow:0 1px 2px rgba(23,32,27,.04),0 18px 40px -20px rgba(23,32,27,.35)}
.art{border-radius:12px;aspect-ratio:4/3;display:grid;place-items:center;margin-bottom:8px}
.art svg{width:58%;height:auto}
[hidden]{display:none!important}
.product-card h3{margin:4px 0 0;font-size:17px;letter-spacing:-.01em}
.tag{font-size:12px;font-weight:700;text-transform:uppercase;letter-spacing:.06em;color:var(--brand-2)}
.row{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:8px}
.price{font-weight:800;font-size:20px;letter-spacing:-.01em}
button,.btn{display:inline-flex;align-items:center;justify-content:center;gap:8px;min-height:46px;padding:0 22px;border-radius:12px;border:0;background:var(--brand);color:#fff;font:600 15px/1 inherit;font-family:inherit;cursor:pointer;text-decoration:none;transition:background .15s,transform .05s}
button:hover,.btn:hover{background:var(--brand-2)}
button:active,.btn:active{transform:translateY(1px)}
.btn-sm{min-height:40px;padding:0 16px;font-size:14px}
button.secondary{background:var(--surface);color:var(--brand);border:1.5px solid var(--brand)}
button.secondary:hover{background:var(--brand-soft)}
label{display:block;font-size:14px;font-weight:600;margin:16px 0 7px}
input{width:100%;min-height:46px;padding:0 14px;border:1.5px solid var(--line);border-radius:11px;font:inherit;background:#fbfaf7}
input:focus{outline:0;border-color:var(--brand);box-shadow:0 0 0 4px var(--brand-soft)}
.banner{padding:14px 16px;border-radius:12px;margin:0 0 18px;font-weight:600;display:flex;gap:10px;align-items:center}
.ok{background:var(--ok-bg);color:var(--ok)}.err{background:var(--err-bg);color:var(--err)}
.auth{display:grid;grid-template-columns:1.1fr 1fr;max-width:920px;margin:24px auto;overflow:hidden}
.auth-art{background:linear-gradient(150deg,#1f5f45,#2c7a59 55%,#e8743b);color:#fff;padding:40px;display:flex;flex-direction:column;justify-content:flex-end;min-height:420px}
.auth-art b{font-size:28px;line-height:1.2;letter-spacing:-.02em}
.auth-art span{opacity:.85;margin-top:8px}
.auth-form{padding:40px}
.auth-form h1{font-size:28px}
.product{display:grid;grid-template-columns:1.05fr 1fr;gap:40px;padding:28px}
.product .art{aspect-ratio:1/1}
.meta{color:var(--muted);font-size:14px;display:flex;gap:16px;flex-wrap:wrap;margin:12px 0 24px}
.actions{display:flex;gap:12px;flex-wrap:wrap}
.reco{margin-top:36px}
.reco h2{font-size:20px;margin:0 0 14px}
.chips{display:flex;gap:10px;flex-wrap:wrap}
.chip{background:var(--surface);border:1px solid var(--line);border-radius:99px;padding:8px 14px;font-size:14px;font-weight:600}
.checkout{display:grid;grid-template-columns:1.4fr 1fr;gap:24px;align-items:start}
.line{display:flex;gap:14px;align-items:center;padding:14px 0;border-bottom:1px solid var(--line)}
.line:last-child{border-bottom:0}
.line .art{width:72px;margin:0;aspect-ratio:1/1;border-radius:10px}
.line .art svg{width:62%}
.summary{padding:24px}
.total{display:flex;justify-content:space-between;font-weight:800;font-size:20px;margin:14px 0 18px}
.muted{color:var(--muted)}
@media (max-width:760px){.auth,.product,.checkout{grid-template-columns:1fr}.auth-art{min-height:200px;padding:28px}.auth-form,.product{padding:24px}.links{gap:14px}.nav{gap:14px}}
`

// productArt is the illustration of each product (inline SVG on its color).
var productArt = map[string][2]string{
	"P-1": {"#f3dfcf", `<svg viewBox="0 0 120 70" aria-hidden="true"><path d="M8 52c6-18 20-30 34-32 6 0 10 6 18 8l26 6c14 3 24 9 26 18v6H8z" fill="#e8743b"/><path d="M8 58h104v6H8z" fill="#17201b"/><path d="M44 26l6 14M54 28l4 12M64 31l2 10" stroke="#fff" stroke-width="3" stroke-linecap="round"/></svg>`},
	"P-2": {"#d6e8de", `<svg viewBox="0 0 100 110" aria-hidden="true"><path d="M30 10h40l20 18-8 14-8-4v62H26V38l-8 4-8-14z" fill="#1f5f45"/><path d="M50 10v92" stroke="#e3efe8" stroke-width="3"/><path d="M40 10c2 8 18 8 20 0" fill="none" stroke="#e3efe8" stroke-width="3"/></svg>`},
	"P-3": {"#e6e1f3", `<svg viewBox="-30 0 120 120" aria-hidden="true"><rect x="18" y="4" width="24" height="12" rx="4" fill="#17201b"/><rect x="10" y="16" width="40" height="100" rx="16" fill="#5b4bb7"/><rect x="10" y="50" width="40" height="22" fill="#e6e1f3" opacity=".55"/></svg>`},
}

func storePage(w http.ResponseWriter, title, body, script string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s · Demo Shop</title><style>%s</style></head><body>
<header><nav class="nav" aria-label="Main">
<a class="brand" href="/products"><span class="brand-mark" aria-hidden="true"><svg width="18" height="18" viewBox="0 0 24 24"><path d="M3 19 10 6l4 7 2-3 5 9z" fill="#fff"/></svg></span>Demo Shop</a>
<div class="links"><a href="/products">Products</a><a href="/cart" id="cart-link" class="cart-link">Cart <span class="cart-count" id="cart-count">0</span></a><a href="/login">Sign in</a></div>
</nav></header>
<main>%s</main><script>
const token = () => sessionStorage.getItem("token");
const api = (path, opts = {}) => fetch(path, { ...opts, headers: { "Content-Type": "application/json", ...(token() ? { Authorization: "Bearer " + token() } : {}), ...(opts.headers || {}) } });
const cartItems = () => JSON.parse(sessionStorage.getItem("cart") || "[]");
document.getElementById("cart-count").textContent = cartItems().length;
const ART = %s;
%s</script></body></html>`, html.EscapeString(title), storeCSS, body, artJSON(), script)
}

// artJSON hands the illustrations to the pages' scripts.
func artJSON() string {
	var b strings.Builder
	b.WriteString("{")
	i := 0
	for id, a := range productArt {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%q:{bg:%q,svg:%q}", id, a[0], a[1])
		i++
	}
	b.WriteString("}")
	return b.String()
}

func productByID(id string) map[string]any {
	for _, p := range products {
		if p["id"] == id {
			return p
		}
	}
	return nil
}

// store serves the shop pages; false when the path is not one of them.
func (s *Shop) store(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch {
	case r.URL.Path == "/login":
		storePage(w, "Sign in", `<div class="card auth">
<div class="auth-art"><b>Gear up for the trail.</b><span>Sign in to check out faster and track your orders.</span></div>
<div class="auth-form"><h1>Sign in</h1><p class="sub">Welcome back to Demo Shop.</p>
<div id="error" class="banner err" role="alert" hidden></div>
<form id="login"><label for="email">Email</label><input id="email" name="email" type="email" value="`+ShopUser+`" autocomplete="username">
<label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password">
<p style="margin-top:22px"><button type="submit" id="sign-in" style="width:100%">Sign in</button></p></form></div></div>`, `
document.getElementById("login").addEventListener("submit", async (e) => {
  e.preventDefault();
  const res = await api("/api/login", { method: "POST", body: JSON.stringify({ email: email.value, password: password.value }) });
  if (!res.ok) { const el = document.getElementById("error"); el.hidden = false; el.textContent = "Invalid email or password"; return; }
  sessionStorage.setItem("token", (await res.json()).accessToken);
  location.href = "/products";
});`)
	case r.URL.Path == "/products":
		// la tarjeta de socio es un dato sensible de mentira: data-tracereports-mask la tapa en toda captura
		storePage(w, "Products", `<h1>Products</h1><p class="sub">Trail-tested gear, shipped free. `+
			`<span class="member" data-tracereports-mask>Member card `+ShopMemberCard+`</span></p><div class="grid" id="products"></div>`, `
api("/api/products").then((r) => r.json()).then(({ products }) => {
  document.getElementById("products").innerHTML = products.map((p) => '<article class="card product-card"><div class="art" style="background:' + ART[p.id].bg + '">' + ART[p.id].svg + '</div><span class="tag">New season</span><h3>' + p.name + '</h3><div class="row"><span class="price">$' + p.price.toFixed(2) + '</span><a class="btn btn-sm" href="/product/' + p.id + '">View</a></div></article>').join("");
});`)
	case strings.HasPrefix(r.URL.Path, "/product/"):
		id := strings.TrimPrefix(r.URL.Path, "/product/")
		p := productByID(id)
		if p == nil {
			http.NotFound(w, r)
			return true
		}
		art := productArt[id]
		storePage(w, p["name"].(string), fmt.Sprintf(`<div class="card product">
<div class="art" style="background:%s">%s</div>
<div><span class="tag">New season</span><h1>%s</h1><p class="price" id="price" style="font-size:26px;margin:8px 0 0">$%.2f</p>
<div class="meta"><span>Free shipping</span><span>30-day returns</span><span>In stock</span></div>
<div id="added" class="banner ok" role="status" hidden>Added to your cart</div>
<div class="actions"><button data-testid="add-to-cart" type="button">Add to cart</button><button class="secondary" id="wishlist" type="button">Save for later</button></div></div></div>
<section class="reco"><h2>You may also like</h2><div id="recommendations" class="muted">Loading…</div></section>`, art[0], art[1], html.EscapeString(p["name"].(string)), p["price"]), fmt.Sprintf(`
document.querySelector('[data-testid="add-to-cart"]').addEventListener("click", () => {
  const cart = cartItems(); cart.push(%q); sessionStorage.setItem("cart", JSON.stringify(cart));
  document.getElementById("cart-count").textContent = cart.length;
  document.getElementById("added").hidden = false;
});
api("/api/recommendations").then(async (r) => {
  const el = document.getElementById("recommendations");
  if (!r.ok) { el.textContent = "Recommendations are not available right now"; el.className = "banner err"; return; }
  el.className = "chips";
  el.innerHTML = (await r.json()).items.map((i) => '<span class="chip">' + i + '</span>').join("");
});`, id))
	case r.URL.Path == "/cart":
		storePage(w, "Cart", `<h1>Your cart</h1><p class="sub">Review your order and pay securely.</p><div id="result" role="status"></div>
<div class="checkout"><section class="card" style="padding:8px 24px" id="items"><p class="muted" style="padding:18px 0">Your cart is empty</p></section>
<aside class="card summary"><h2 style="margin:0;font-size:18px">Payment</h2>
<label for="card">Card number</label><input id="card" value="4242 4242 4242 4242" inputmode="numeric">
<div class="total"><span>Total</span><span id="total">$0.00</span></div>
<button id="pay" type="button" style="width:100%">Pay now</button></aside></div>`, `
const cart = cartItems();
const PRICES = { "P-1": ["Trail running shoes", 89.9], "P-2": ["Waterproof jacket", 129], "P-3": ["Insulated bottle", 24.5] };
if (cart.length) {
  document.getElementById("items").innerHTML = cart.map((id) => '<div class="line"><div class="art" style="background:' + ART[id].bg + '">' + ART[id].svg + '</div><div style="flex:1"><b>' + PRICES[id][0] + '</b><div class="muted">Qty 1</div></div><b>$' + PRICES[id][1].toFixed(2) + '</b></div>').join("");
  document.getElementById("total").textContent = "$" + cart.reduce((t, id) => t + PRICES[id][1], 0).toFixed(2);
}
document.getElementById("pay").addEventListener("click", async () => {
  const res = await api("/api/orders", { method: "POST", body: JSON.stringify({ items: cart.map((id) => ({ id, qty: 1 })), payment: { method: "card", last4: card.value.slice(-4) } }) });
  const out = document.getElementById("result");
  if (res.ok) { const o = await res.json(); out.innerHTML = '<div class="banner ok order-confirmed">Order confirmed · ' + o.id + '</div>'; return; }
  out.innerHTML = '<div class="banner err">Something went wrong. Please try again later.</div>';
  const o = await res.json(); console.error("Order failed: " + o.error); renderConfirmation(o.order);
});
function renderConfirmation(order) { document.title = order.id; }`)
	default:
		return false
	}
	return true
}
