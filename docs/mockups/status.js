/* The static equivalent of Led.tsx: a hidden lamp and an adjacent word.
   Explicitly update both when a simulation changes state. Never auto-label
   arbitrary markup: that would hide a missing alternative from the guards. */
function mockupStatusWord(state, stale = false) {
  const words = { up: "Up", warn: "Warning", down: "Down", idle: "Waiting", off: "Paused" };
  const word = words[state];
  if (!word) throw new Error(`Unknown mockup LED state: ${state}`);
  return stale && ["up", "warn", "down"].includes(state) ? `Was ${word.toLowerCase()}` : word;
}

function setMockupLed(led, state, stale = document.body.dataset.conn === "stale") {
  const label = led.nextElementSibling;
  if (!label?.matches(".sr-only, .led-label")) throw new Error("LED needs an adjacent textual alternative");
  led.dataset.state = state;
  label.textContent = mockupStatusWord(state, stale);
}
