import * as Sabab from "@sabab/browser";

// The dev DSN. The public key ships in browser bundles by design — it is
// write-only, rate-limited and revocable. Point it at the local gateway.
Sabab.init({
	dsn: "http://pk_live_5ea720bd0e5ef61a1a4c1df473323f16@localhost:8090/1",
	environment: "demo",
	release: "demo-shop@1.0.0",
	serviceName: "demo-shop",
	debug: true,
});
Sabab.setUser({ id: "u_demo", email: "shopper@example.com" });

const out = document.querySelector<HTMLDivElement>("#log")!;
let n = 0;
const say = (msg: string) => {
	out.textContent = `${String(++n).padStart(2, "0")}  ${msg}\n${out.textContent}`;
};
say("SDK initialised — Web Vitals collecting.");

function on(id: string, fn: () => void) {
	document.querySelector<HTMLButtonElement>(`#${id}`)!.addEventListener("click", fn);
}

on("handled", () => {
	try {
		// A realistic bug: calling a method on something undefined.
		const cart: { total?: () => number } = {};
		(cart.total as () => number)();
	} catch (err) {
		Sabab.captureException(err);
		say("captured a handled TypeError");
	}
});

on("uncaught", () => {
	say("throwing — watch it land in Issues");
	// Thrown async so it reaches window.onerror rather than this click handler.
	setTimeout(() => {
		throw new Error("Checkout failed: payment gateway timeout");
	}, 0);
});

on("logs", () => {
	Sabab.log.info("checkout started", { cartSize: 3, currency: "USD" });
	Sabab.log.warn("inventory low", { sku: "SKU-42", left: 2 });
	Sabab.log.error("payment declined", { code: "card_declined" });
	void Sabab.flush();
	say("sent 3 logs (info, warn, error)");
});

on("metrics", () => {
	Sabab.metrics.increment("checkout.completed", 1, { tags: { plan: "pro" } });
	Sabab.metrics.distribution("checkout.total", Math.round(20 + Math.random() * 180), { unit: "usd" });
	const done = Sabab.metrics.startTimer("checkout.duration", { tags: { step: "pay" } });
	setTimeout(() => {
		done();
		void Sabab.flush();
		say("sent a counter, a distribution and a timing");
	}, 180);
});

on("vitals", () => {
	// Web Vitals finalise on the first visibilitychange to hidden. Simulate it so
	// the demo does not require actually switching tabs.
	Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
	document.dispatchEvent(new Event("visibilitychange"));
	say("flushed Web Vitals (LCP, CLS, INP, FCP, TTFB)");
});
