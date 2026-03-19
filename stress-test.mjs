import { chromium } from "playwright";

const SERVER_URL = "https://webtransportdemo.duckdns.org";
const N = 100;
const RAMP_MS = 5000;

const browser = await chromium.launch({ headless: false });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true });

async function runClient(i) {
    await new Promise(r => setTimeout(r, (i / N) * RAMP_MS));
    const page = await ctx.newPage();
    await page.goto(SERVER_URL);
    await page.evaluate(() => {
        let t = Math.random() * Math.PI * 2;
        const cx = Math.random() * 1920;
        const cy = Math.random() * 1080;
        const r = 50 + Math.random() * 100;

        setInterval(() => {
            t += 0.05;
            const x = cx + Math.cos(t) * r;
            const y = cy + Math.sin(t) * r;
            document.dispatchEvent(new MouseEvent("mousemove", { clientX: x, clientY: y, bubbles: true }));
        }, 100);
    });
}

await Promise.all(Array.from({ length: N }, (_, i) => runClient(i)));
console.log(`${N} tabs running — Ctrl+C to stop`);
await new Promise(() => {});