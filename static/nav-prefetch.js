(() => {
  const prefetched = new Set();

  function addPrefetch(url, as) {
    if (!url || prefetched.has(url)) {
      return;
    }
    prefetched.add(url);

    const link = document.createElement('link');
    link.rel = 'prefetch';
    link.href = url;
    if (as) {
      link.as = as;
    }
    document.head.appendChild(link);
  }

  function prefetchFromBodyHint() {
    const hint = document.body.dataset.prefetch;
    if (!hint) {
      return;
    }
    hint.split(',').map((s) => s.trim()).forEach((url) => {
      if (url.endsWith('.css')) {
        addPrefetch(url, 'style');
      } else if (url.endsWith('.js')) {
        addPrefetch(url, 'script');
      } else {
        addPrefetch(url, 'document');
      }
    });
  }

  function bindNavPrefetch() {
    const navLinks = document.querySelectorAll('.top-nav .nav-link[href]');
    navLinks.forEach((linkEl) => {
      const handler = () => addPrefetch(linkEl.getAttribute('href'), 'document');
      linkEl.addEventListener('pointerenter', handler, { once: true });
      linkEl.addEventListener('focus', handler, { once: true });
      linkEl.addEventListener('touchstart', handler, { once: true, passive: true });
    });
  }

  function idlePrefetch() {
    const run = () => prefetchFromBodyHint();
    if ('requestIdleCallback' in globalThis) {
      globalThis.requestIdleCallback(run, { timeout: 1200 });
    } else {
      globalThis.setTimeout(run, 200);
    }
  }

  bindNavPrefetch();
  idlePrefetch();
})();
