package tracereports

import "strings"

// Capturas sin datos sensibles. El cliente de Go no toma capturas (las toma tu código con
// playwright-go, chromedp o Selenium); te da qué tapar y cómo, y respeta la política:
//
//   - TRACEREPORTS_SCREENSHOT_MASK: selectores CSS a tapar (un grupo CSS: "input[name=rut], .tarjeta").
//   - Siempre se tapan input[type=password] y los elementos con el atributo data-tracereports-mask;
//     TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS=off lo desactiva.
//   - TRACEREPORTS_SCREENSHOTS: all (por defecto), failures (solo la captura de un paso FAIL) u off
//     (ninguna). Test.Screenshot no envía lo que la política no permite; el servidor aplica la misma.
//
// Con playwright-go:
//
//	png, err := page.Screenshot(playwright.PageScreenshotOptions{
//		Mask:      []playwright.Locator{page.Locator(tracereports.MaskSelector())},
//		MaskColor: playwright.String(tracereports.MaskColor),
//	})
//
// Sin opción de máscara (chromedp, Selenium): ejecuta CoverScript(MaskSelector(), MaskColor,
// MaskOverlayAttr) antes de capturar y UncoverScript(MaskOverlayAttr) después, siempre.

// DefaultMask is what is always covered (unless TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS=off).
const DefaultMask = "input[type=password], [data-tracereports-mask]"

// MaskColor is the color of the boxes: Playwright's default.
const MaskColor = "#FF00FF"

// MaskOverlayAttr marks the boxes CoverScript adds, so UncoverScript removes them all.
const MaskOverlayAttr = "data-tracereports-mask-overlay"

// CoverScript puts a fixed box over every visible element of the selector (the viewport is what a
// screenshot shows). Arguments: selector, color, attribute. It returns how many it covered.
const CoverScript = `const [sel, color, attr] = arguments;
let n = 0;
for (const el of document.querySelectorAll(sel)) {
  const r = el.getBoundingClientRect();
  if (!r.width || !r.height) continue;
  const d = document.createElement("div");
  d.setAttribute(attr, "");
  d.style.cssText = "position:fixed;left:" + r.left + "px;top:" + r.top + "px;width:" + r.width + "px;height:" + r.height + "px;" +
    "background:" + color + ";z-index:2147483647;pointer-events:none;margin:0;padding:0;border:0";
  document.documentElement.appendChild(d);
  n++;
}
return n;`

// UncoverScript removes the boxes of CoverScript. Argument: the attribute.
const UncoverScript = `for (const d of document.querySelectorAll("[" + arguments[0] + "]")) d.remove();`

// MaskSelector is the CSS selector group to cover ("" = nothing).
func MaskSelector() string {
	var parts []string
	switch strings.ToLower(strings.TrimSpace(getenv("SCREENSHOT_MASK_DEFAULTS"))) {
	case "off", "0", "false", "no":
	default:
		parts = append(parts, DefaultMask)
	}
	if extra := strings.TrimSpace(strings.Trim(strings.TrimSpace(getenv("SCREENSHOT_MASK")), ",")); extra != "" {
		parts = append(parts, extra)
	}
	return strings.Join(parts, ", ")
}

// ScreenshotsPolicy is TRACEREPORTS_SCREENSHOTS: all, failures or off (unknown = all).
func ScreenshotsPolicy() string {
	switch p := strings.ToLower(strings.TrimSpace(getenv("SCREENSHOTS"))); p {
	case "failures", "off":
		return p
	}
	return "all"
}

// ScreenshotAllowed reports whether the policy lets a screenshot of a step with this status be sent.
func ScreenshotAllowed(status string) bool {
	switch ScreenshotsPolicy() {
	case "off":
		return false
	case "failures":
		return strings.EqualFold(status, Fail)
	}
	return true
}
