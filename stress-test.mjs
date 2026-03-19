import { chromium } from "playwright";

const SERVER_URL = "https://webtransportdemo.duckdns.org";
const N = 80;
const RAMP_MS = 5000;

const browser = await chromium.launch({ headless: false });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true });

async function runClient(i) {
    await new Promise(r => setTimeout(r, (i / N) * RAMP_MS));
    const page = await ctx.newPage();
    await page.goto(SERVER_URL);

    await page.evaluate(() => {
        let x = Math.random() * 1920;
        let y = Math.random() * 1080;
        let dx = (Math.random() - 0.5) * 20;
        let dy = (Math.random() - 0.5) * 20;

        setInterval(() => {
            dx += (Math.random() - 0.5) * 4;
            dy += (Math.random() - 0.5) * 4;
            dx = Math.max(-30, Math.min(30, dx));
            dy = Math.max(-30, Math.min(30, dy));
            x = Math.max(0, Math.min(1919, x + dx));
            y = Math.max(0, Math.min(1079, y + dy));
            if (x <= 0 || x >= 1919) dx = -dx;
            if (y <= 0 || y >= 1079) dy = -dy;

            const e = new MouseEvent("mousemove", { clientX: x, clientY: y, bubbles: true });
            document.dispatchEvent(e);
        }, 100);
    });
}

await Promise.all(Array.from({ length: N }, (_, i) => runClient(i)));
console.log(`${N} tabs running — Ctrl+C to stop`);
await new Promise(() => {});