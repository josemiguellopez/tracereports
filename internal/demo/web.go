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

const storeCSS = `*{box-sizing:border-box}body{margin:0;font-family:system-ui,-apple-system,"Segoe UI",sans-serif;background:#f7f8fa;color:#1f2937}
header{background:#1f2937;color:#fff;display:flex;align-items:center;justify-content:space-between;padding:14px 32px}
header a{color:#fff;text-decoration:none;margin-left:20px;font-size:14px}.logo{font-weight:800;color:#f97316!important;margin:0!important;font-size:18px}
main{max-width:980px;margin:32px auto;padding:0 20px}h1{font-size:26px;margin:0 0 20px}
.card{background:#fff;border:1px solid #e5e7eb;border-radius:10px;padding:22px}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:18px}
.thumb{height:140px;border-radius:8px;background:linear-gradient(135deg,#e5e7eb,#d1d5db);margin-bottom:14px}
.price{font-weight:700;color:#111827}.muted{color:#6b7280;font-size:14px}
button,.btn{display:inline-block;padding:11px 18px;border-radius:8px;border:0;background:#2563eb;color:#fff;font-weight:600;font-size:15px;cursor:pointer;text-decoration:none}
button.secondary{background:#fff;color:#2563eb;border:1px solid #2563eb}
label{display:block;font-size:14px;font-weight:600;margin:14px 0 6px}input{width:100%;padding:10px 12px;border:1px solid #d1d5db;border-radius:8px;font-size:15px}
.banner{padding:14px 18px;border-radius:8px;margin-bottom:18px;font-weight:600}.ok{background:#dcfce7;color:#166534}.err{background:#fee2e2;color:#991b1b}
.row{display:flex;gap:12px;align-items:center}.reco{margin-top:28px}`

func storePage(w http.ResponseWriter, title, body, script string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s · Demo Shop</title><style>%s</style></head><body>
<header><a class="logo" href="/products">Demo Shop</a><nav><a href="/products">Products</a><a href="/cart" id="cart-link">Cart</a><a href="/login">Sign in</a></nav></header>
<main>%s</main><script>
const token = () => sessionStorage.getItem("token");
const api = (path, opts = {}) => fetch(path, { ...opts, headers: { "Content-Type": "application/json", ...(token() ? { Authorization: "Bearer " + token() } : {}), ...(opts.headers || {}) } });
%s</script></body></html>`, html.EscapeString(title), storeCSS, body, script)
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
		storePage(w, "Sign in", `<div class="card" style="max-width:420px;margin:auto">
<h1>Sign in</h1><div id="error" class="banner err" hidden></div>
<form id="login"><label for="email">Email</label><input id="email" name="email" type="email" value="`+ShopUser+`" autocomplete="username">
<label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password">
<p><button type="submit" id="sign-in">Sign in</button></p></form></div>`, `
document.getElementById("login").addEventListener("submit", async (e) => {
  e.preventDefault();
  const res = await api("/api/login", { method: "POST", body: JSON.stringify({ email: email.value, password: password.value }) });
  if (!res.ok) { const el = document.getElementById("error"); el.hidden = false; el.textContent = "Invalid email or password"; return; }
  sessionStorage.setItem("token", (await res.json()).accessToken);
  location.href = "/products";
});`)
	case r.URL.Path == "/products":
		storePage(w, "Products", `<h1>Products</h1><div class="grid" id="products"></div>`, `
api("/api/products").then((r) => r.json()).then(({ products }) => {
  document.getElementById("products").innerHTML = products.map((p) => '<div class="card product-card"><div class="thumb"></div><h3>' + p.name + '</h3><p class="price">$' + p.price.toFixed(2) + '</p><a class="btn" href="/product/' + p.id + '">View</a></div>').join("");
});`)
	case strings.HasPrefix(r.URL.Path, "/product/"):
		p := productByID(strings.TrimPrefix(r.URL.Path, "/product/"))
		if p == nil {
			http.NotFound(w, r)
			return true
		}
		storePage(w, p["name"].(string), fmt.Sprintf(`<div class="card"><div class="row" style="align-items:flex-start;gap:28px">
<div class="thumb" style="width:320px;height:240px"></div><div><h1>%s</h1><p class="price" id="price">$%.2f</p>
<p class="muted">Free shipping · 30-day returns</p><div id="added" class="banner ok" hidden>Added to your cart</div>
<div class="row"><button data-testid="add-to-cart" type="button">Add to cart</button><button class="secondary" id="wishlist" type="button">Save for later</button></div></div></div>
<div class="reco"><h3>You may also like</h3><div id="recommendations" class="muted">Loading…</div></div></div>`, html.EscapeString(p["name"].(string)), p["price"]), fmt.Sprintf(`
document.querySelector('[data-testid="add-to-cart"]').addEventListener("click", () => {
  const cart = JSON.parse(sessionStorage.getItem("cart") || "[]"); cart.push(%q); sessionStorage.setItem("cart", JSON.stringify(cart));
  document.getElementById("added").hidden = false;
});
api("/api/recommendations").then(async (r) => {
  const el = document.getElementById("recommendations");
  if (!r.ok) { el.textContent = "Recommendations are not available right now"; el.className = "banner err"; return; }
  el.textContent = (await r.json()).items.join(" · ");
});`, p["id"]))
	case r.URL.Path == "/cart":
		storePage(w, "Cart", `<h1>Your cart</h1><div id="result"></div><div class="card"><div id="items" class="muted">Your cart is empty</div>
<label for="card">Card number</label><input id="card" value="4242 4242 4242 4242">
<p><button id="pay" type="button">Pay now</button></p></div>`, `
const cart = JSON.parse(sessionStorage.getItem("cart") || "[]");
if (cart.length) document.getElementById("items").textContent = cart.length + " item(s): " + cart.join(", ");
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
