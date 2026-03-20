import { chromium } from "playwright";

const SERVER_URL = "https://webtransportdemo.duckdns.org";
const N = 20;
const RAMP_MS = 5000;

const VIEWPORTS = [
    { width: 1920, height: 1080 },
    { width: 1280, height: 720 },
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
    { width: 768, height: 1024 },
];

const browser = await chromium.launch({
    headless: true,
    args: [
        "--no-sandbox",
        "--disable-setuid-sandbox",
        "--disable-gpu",
        "--disable-dev-shm-usage",
        "--disable-software-rasterizer",

        "--disable-background-networking",
        "--ignore-certificate-errors",
        "--js-flags=--max-old-space-size=256",

        "--disable-extensions",
        "--disable-plugins",
        "--disable-default-apps",
        "--disable-sync",
        "--disable-translate",
        "--disable-features=TranslateUI,BlinkGenPropertyTrees,AudioServiceOutOfProcess",
        "--disable-background-timer-throttling",
        "--disable-backgrounding-occluded-windows",
        "--disable-renderer-backgrounding",
        "--disable-hang-monitor",
        "--disable-prompt-on-repost",
        "--disable-client-side-phishing-detection",
        "--disable-component-update",
        "--disable-domain-reliability",
        "--disable-ipc-flooding-protection",
        "--disable-audio-output",
        "--mute-audio",
        "--disable-web-security",

        "--memory-pressure-off",
        "--disable-histogram-customizer",

        "--log-level=3",
        "--silent-debugger-extension-api",
    ],
});
const contextCache = new Map();
async function getContext(viewport) {
    const key = `${viewport.width}x${viewport.height}`;
    if (!contextCache.has(key)) {
        contextCache.set(
            key,
            await browser.newContext({
                ignoreHTTPSErrors: true,
                viewport,
            }),
        );
    }
    return contextCache.get(key);
}

async function runClient(i) {
    await new Promise((r) => setTimeout(r, (i / N) * RAMP_MS));
    const viewport = VIEWPORTS[i % VIEWPORTS.length];
    const ctx = await getContext(viewport);
    const page = await ctx.newPage();
    await page.goto(SERVER_URL);
    await page.evaluate(() => {
        let t = Math.random() * Math.PI * 2;
        const cx = window.innerWidth * (0.2 + Math.random() * 0.6);
        const cy = window.innerHeight * (0.2 + Math.random() * 0.6);
        const r = 40 + Math.random() * 80;
        setInterval(() => {
            t += 0.05;
            const x = cx + Math.cos(t) * r;
            const y = cy + Math.sin(t) * r;
            document.dispatchEvent(
                new MouseEvent("mousemove", {
                    clientX: x,
                    clientY: y,
                    bubbles: true,
                }),
            );
        }, 100);
    });
}

await Promise.all(Array.from({ length: N }, (_, i) => runClient(i)));
console.log(`${N} tabs across ${contextCache.size} contexts — Ctrl+C to stop`);

process.on("SIGINT", async () => {
    console.log("\nshutting down...");
    await browser.close();
    process.exit(0);
});

await new Promise(() => {});
