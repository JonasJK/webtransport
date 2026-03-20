(() => {
  const gridCanvas = document.getElementById('grid-bg');
  const lightCanvas = document.getElementById('lights-bg');
  const selfCursor = document.getElementById('self');

  if (!gridCanvas || !lightCanvas || !selfCursor) {
    return;
  }

  const gc = gridCanvas.getContext('2d');
  const lc = lightCanvas.getContext('2d');

  const DOT_SPACING = 28;
  const PUSH_RADIUS = 90;
  const PUSH_FORCE = 0.38;
  const SPRING = 0.12;
  const DAMPEN = 0.72;
  const LIGHT_RADIUS = 160;

  let width = 0;
  let height = 0;
  let dots = [];
  let selfX = -300;
  let selfY = -300;

  function resize() {
    width = window.innerWidth;
    height = window.innerHeight;
    gridCanvas.width = width;
    gridCanvas.height = height;
    lightCanvas.width = width;
    lightCanvas.height = height;

    dots = [];
    for (let row = 0; row <= Math.ceil(height / DOT_SPACING); row++) {
      for (let col = 0; col <= Math.ceil(width / DOT_SPACING); col++) {
        dots.push({
          bx: col * DOT_SPACING,
          by: row * DOT_SPACING,
          x: col * DOT_SPACING,
          y: row * DOT_SPACING,
          bright: 0,
          vx: 0,
          vy: 0,
        });
      }
    }
  }

  function setPos(x, y) {
    selfX = x;
    selfY = y;
    selfCursor.style.left = `${x}px`;
    selfCursor.style.top = `${y}px`;
  }

  document.addEventListener('mousemove', (e) => setPos(e.clientX, e.clientY));
  document.addEventListener('touchstart', (e) => {
    const t = e.touches[0];
    setPos(t.clientX, t.clientY);
  }, { passive: true });
  document.addEventListener('touchmove', (e) => {
    const t = e.touches[0];
    setPos(t.clientX, t.clientY);
  }, { passive: true });

  function draw() {
    requestAnimationFrame(draw);

    for (const d of dots) {
      const dx = d.bx - selfX;
      const dy = d.by - selfY;
      const dist = Math.hypot(dx, dy);

      let fx = 0;
      let fy = 0;
      let maxBright = 0;

      if (dist < LIGHT_RADIUS) {
        maxBright = (1 - dist / LIGHT_RADIUS) ** 2;
      }

      if (dist > 0 && dist < PUSH_RADIUS) {
        const s = (1 - dist / PUSH_RADIUS) * PUSH_FORCE;
        fx += (dx / dist) * s;
        fy += (dy / dist) * s;
      }

      d.vx = (d.vx + fx + (d.bx - d.x) * SPRING) * DAMPEN;
      d.vy = (d.vy + fy + (d.by - d.y) * SPRING) * DAMPEN;
      d.x += d.vx;
      d.y += d.vy;
      d.bright += (maxBright - d.bright) * 0.15;
    }

    gc.clearRect(0, 0, width, height);
    for (const d of dots) {
      gc.beginPath();
      gc.arc(d.x, d.y, 1.4 + d.bright * 1.6, 0, Math.PI * 2);
      gc.fillStyle = `rgba(255,255,255,${0.08 + d.bright * 0.5})`;
      gc.fill();
    }

    lc.clearRect(0, 0, width, height);
    if (selfX > -200) {
      const g = lc.createRadialGradient(
        selfX,
        selfY,
        0,
        selfX,
        selfY,
        LIGHT_RADIUS * 1.1,
      );
      g.addColorStop(0, 'rgba(255,255,255,0.06)');
      g.addColorStop(0.4, 'rgba(255,255,255,0.03)');
      g.addColorStop(1, 'rgba(255,255,255,0)');

      lc.beginPath();
      lc.arc(selfX, selfY, LIGHT_RADIUS * 1.1, 0, Math.PI * 2);
      lc.fillStyle = g;
      lc.fill();
    }
  }

  window.addEventListener('resize', resize);
  resize();
  draw();
})();
