// Adds the benchmark monitors to a fresh Uptime Kuma through its own socket
// API, the same calls its web interface makes. Runs inside the Kuma
// container (`docker exec ... node /bench/seed-kuma.js`), which already has
// socket.io-client installed, and works on 1.23 and 2.x.
//
// Environment: MONITORS, INTERVAL and TIMEOUT (seconds), TARGET_PREFIX (the
// URL each monitor checks, with its number appended).
const { io } = require("/app/node_modules/socket.io-client");

const version = require("/app/package.json").version;
const major = Number(version.split(".")[0]);
const count = Number(process.env.MONITORS);
const interval = Number(process.env.INTERVAL);
const timeout = Number(process.env.TIMEOUT);
const prefix = process.env.TARGET_PREFIX;

// Every field the 1.23 edit form sends. 2.x refuses a monitor without the
// fields it added, and 1.23 refuses one with them, so they are added by version.
const base = {
  type: "http", method: "GET", body: null, headers: null,
  interval, retryInterval: interval, resendInterval: 0, maxretries: 0, timeout,
  notificationIDList: {}, ignoreTls: false, upsideDown: false, packetSize: 56,
  expiryNotification: false, maxredirects: 10, accepted_statuscodes: ["200-299"],
  dns_resolve_type: "A", dns_resolve_server: "1.1.1.1", docker_container: "", docker_host: null,
  proxyId: null, mqttUsername: "", mqttPassword: "", mqttTopic: "", mqttSuccessMessage: "",
  authMethod: null, oauth_auth_method: "client_secret_basic", httpBodyEncoding: "json",
  kafkaProducerBrokers: [], kafkaProducerSaslOptions: { mechanism: "None" }, kafkaProducerSsl: false,
  kafkaProducerAllowAutoTopicCreation: false, gamedigGivenPortOnly: true, active: true,
  description: "", keyword: "", invertKeyword: false, hostname: "", port: null,
};
if (major >= 2) {
  Object.assign(base, { conditions: [], rabbitmqNodes: [], jsonPathOperator: "==" });
}

const s = io("http://localhost:3001", { transports: ["websocket"] });
// Every call has a deadline: an event Kuma never answers fails the run
// instead of hanging it.
const call = (event, ...args) =>
  new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(event + ": no answer in 30s")), 30000);
    s.emit(event, ...args, (r) => {
      clearTimeout(timer);
      if (r && r.ok === false) reject(new Error(event + ": " + r.msg));
      else resolve(r);
    });
  });

(async () => {
  await new Promise((resolve) => s.on("connect", resolve));
  // Kuma 2 registers its event handlers after an await in its connection
  // handler, so an event sent the moment the socket connects goes unanswered.
  await new Promise((resolve) => setTimeout(resolve, 2000));
  const needSetup = await call("needSetup");
  if (needSetup) await call("setup", "admin", "benchmark-password-1");
  await call("login", { username: "admin", password: "benchmark-password-1", token: "" });
  for (let i = 1; i <= count; i++) {
    await call("add", { ...base, name: "Benchmark " + i, url: prefix + i });
  }
  console.log("Uptime Kuma " + version + ": " + count + " monitors added");
  s.close();
})().catch((e) => {
  console.error(e.message);
  process.exit(1);
});
