import { useState, type CSSProperties } from "react";
import { Link } from "react-router";
import { Footer, Header } from "~/components/chrome";
import { GITHUB, SITE_URL, VERSION, seo } from "~/lib/site";
import type { Route } from "./+types/home";

const DESCRIPTION =
  "Self-hosted PaaS that leaves your server to your apps: deploy apps and databases with restore-tested backups, in about 15 MB of RAM.";

export const meta: Route.MetaFunction = () => [
  ...seo({ title: "kipitiny · a small self-hosted PaaS", description: DESCRIPTION, path: "/" }),
  {
    "script:ld+json": {
      "@context": "https://schema.org",
      "@type": "SoftwareApplication",
      name: "kipitiny",
      description: DESCRIPTION,
      url: SITE_URL,
      applicationCategory: "DeveloperApplication",
      operatingSystem: "Linux (Docker)",
      ...(VERSION && { softwareVersion: VERSION }),
      offers: { "@type": "Offer", price: "0", priceCurrency: "EUR" },
      codeRepository: GITHUB,
    },
  },
];

const INSTALL = `curl -fsSLO https://raw.githubusercontent.com/MatHoyer/kipitiny/main/docker-compose.yml
docker compose up -d`;

/** Position of a tick on the 0–2048 MB axis. */
const at = (mb: number) => ({ "--at": mb }) as CSSProperties;

function InstallCommand() {
  const [copied, setCopied] = useState(false);
  const copy = () =>
    navigator.clipboard.writeText(INSTALL).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    });
  return (
    <div className="install">
      <pre>
        {INSTALL.split("\n").map((line, i) => (
          <span key={i}>
            {i > 0 && "\n"}
            <span className="p">$ </span>
            {line}
          </span>
        ))}
      </pre>
      <button className="copy" type="button" onClick={copy}>
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

export default function Home() {
  return (
    <div className="wrap">
      <Header />
      <main>
        <section className="hero">
          <h1>Your server is for your apps.</h1>
          <p className="lede">Not for the platform that runs them. kipitiny is a self-hosted PaaS: it deploys your <strong>apps</strong> and <strong>databases</strong>, keeps them backed up, and stays out of the way: about 15&nbsp;MB of memory and no CPU at rest.</p>
          <InstallCommand />
          <p className="hero-links">Then open <code>http://your-server:3000</code>. Setting up a fresh server? Use the <a href="https://github.com/MatHoyer/kipitiny/tree/main/deploy/ansible">Ansible playbook</a>.</p>

          <figure className="scale" aria-labelledby="scale-title">
            <h2 id="scale-title">A 2&nbsp;GB server, drawn to scale</h2>
            <p>The platform takes the thin strip on the left. Everything else goes to what you deploy.</p>
            <div className="server" role="img" aria-label="Of 2048 megabytes, kipitiny uses about 15 and Traefik about 100; about 1.9 gigabytes stay free for your apps and databases.">
              <div className="seg mgr"></div>
              <div className="seg proxy"></div>
              <div className="seg yours"></div>
              <span className="val ours">kipitiny + Traefik</span>
              <span className="val free">~1.9 GB free for your apps</span>
            </div>
            <div className="ticks"><ol aria-hidden="true"><li style={at(0)}>0</li><li className="odd" style={at(512)}>512</li><li style={at(1024)}>1024</li><li className="odd" style={at(1536)}>1536</li><li style={at(2048)}>2048 MB</li></ol></div>
            <p className="note">Measured with <code>docker stats</code> on a running install: manager ~15&nbsp;MB, Traefik ~100&nbsp;MB, 0% CPU while nothing is happening.</p>
          </figure>
        </section>

        <section className="block" id="backups">
          <h2>Backups you have actually restored</h2>
          <p className="intro">A backup nobody restored is a hope. Every database backup goes through the same four steps, and schedules restore-test by default.</p>
          <ol className="pipeline">
            <li>
              <h3>Dump at the source</h3>
              <p>The dump runs inside the database container with its own tools, so versions always match, and streams straight to storage. Memory stays flat.</p>
            </li>
            <li>
              <h3>Record what was written</h3>
              <p>A backup only counts once the dump succeeds. Size, SHA-256, server version and duration are kept; partial uploads are deleted.</p>
            </li>
            <li>
              <h3>Restore it somewhere safe</h3>
              <p>A throwaway container of the same version, with no network, loads the backup and reads it back. What it found is recorded on the backup.</p>
            </li>
            <li>
              <h3>Swap it in when needed</h3>
              <p>A restore is staged next to the live data and swapped in only once complete. If it fails, nothing live was touched.</p>
            </li>
          </ol>

          <div className="targets">
            <div>
              <h3>Where they go</h3>
              <p>Local disk or any S3-compatible bucket. Targets are tested with a write before they're saved.</p>
            </div>
            <div>
              <h3>Encrypted with age</h3>
              <p>Remote targets can encrypt everything. Without kipitiny, <code>age -d</code> and the database's own restore tool still get your data back.</p>
            </div>
            <div>
              <h3>Kept on a schedule</h3>
              <p>Cron schedules with retention by count, day, week and month. Volumes and the manager's own state are backed up too.</p>
            </div>
          </div>
        </section>

        <section className="block">
          <h2>What it runs</h2>
          <p className="intro">Projects group services on one server. Everything is a regular Docker container you can still inspect yourself.</p>
          <dl className="runs">
            <div><dt>Apps</dt><dd>From any image, public or private registry. HTTPS on your domain through Traefik, 1–10 replicas, health-gated deploys and rollback to an earlier image.</dd></div>
            <div><dt>Databases</dt><dd>Created in one step with generated credentials and their own volume. Apps reference a database as <code>{"{{ db.main.URL }}"}</code> instead of a copied password.</dd></div>
            <div><dt>Published ports</dt><dd>Non-HTTP traffic straight to a container: a game server on UDP, MQTT, Minecraft on <code>25565</code>.</dd></div>
            <div><dt>Compose projects</dt><dd>Import or export a <code>docker-compose.yml</code>, or keep a project in sync with a file in git.</dd></div>
            <div><dt>Several servers</dt><dd>Remote Docker hosts over SSH, with no daemon listening on TCP. Deploys, logs and backups work the same everywhere.</dd></div>
            <div><dt>CI and agents</dt><dd>Scoped API tokens for deploys from CI, a built-in MCP endpoint for coding agents, and an audit log of every change.</dd></div>
          </dl>
        </section>

        <section className="block">
          <div className="one">
            <div>
              <h2>One process, nothing beside it</h2>
              <p className="intro">The manager is a single static binary with the web UI embedded. It talks to the Docker daemon on the host and keeps its state in one SQLite file, which it backs up like everything else.</p>
              <ul className="not">
                <li><span>–</span><span>No database server or cache for its own state</span></li>
                <li><span>–</span><span>No queue, no worker containers</span></li>
                <li><span>–</span><span>No Node.js at runtime</span></li>
                <li><span>–</span><span>Updates itself from the UI; apps and databases keep running</span></li>
              </ul>
            </div>
            <svg className="diagram" viewBox="0 0 420 330" role="img" aria-label="The kipitiny manager container holds the Go binary, the embedded UI and SQLite. It drives the host Docker socket, which runs Traefik, your apps and your databases.">
              <rect className="dash" x="1" y="1" width="418" height="328" rx="10"/>
              <text className="s" x="16" y="24">your server</text>
              <rect className="hi" x="24" y="40" width="200" height="104" rx="8"/>
              <text className="t on-hi" x="40" y="66">kipitiny</text>
              <text className="on-hi" x="40" y="90">Go binary + web UI</text>
              <text className="on-hi" x="40" y="110">SQLite state</text>
              <text className="on-hi" x="40" y="130">cron, backups, deploys</text>
              <rect className="box" x="268" y="66" width="128" height="52" rx="8"/>
              <text x="332" y="97" textAnchor="middle">docker.sock</text>
              <path className="ln" d="M224 92 H268"/>
              <path className="ln" d="M332 118 V170"/>
              <path className="ln" d="M83 170 H332"/>
              <path className="ln" d="M83 170 V196 M206 170 V196 M332 170 V196"/>
              <rect className="box" x="24" y="196" width="118" height="56" rx="8"/>
              <text x="83" y="229" textAnchor="middle">Traefik</text>
              <rect className="box" x="152" y="196" width="108" height="56" rx="8"/>
              <text x="206" y="229" textAnchor="middle">apps</text>
              <rect className="box" x="269" y="196" width="126" height="56" rx="8"/>
              <text x="332" y="229" textAnchor="middle">databases</text>
              <text className="s" x="24" y="290">Sibling containers, labelled kipitiny.*</text>
              <text className="s" x="24" y="308">No Docker-in-Docker, no privileged mode.</text>
            </svg>
          </div>
        </section>

        <section className="block" id="install">
          <h2>Install</h2>
          <p className="intro">Any Linux server with Docker. The UI and API require a login, since access to the Docker socket is root on the host.</p>
          <ol className="steps">
            <li>
              <div>
                <h3>Start the manager</h3>
                <pre>{`curl -fsSLO https://raw.githubusercontent.com/MatHoyer/kipitiny/main/docker-compose.yml
    docker compose up -d`}</pre>
              </div>
            </li>
            <li>
              <div>
                <h3>Read the setup token</h3>
                <p>It's printed once in the manager's log.</p>
                <pre>{`docker compose logs manager | grep setup_token`}</pre>
              </div>
            </li>
            <li>
              <div>
                <h3>Create the admin account</h3>
                <p>Open <code>http://your-server:3000</code>, paste the token, choose a password. Two-factor and passkeys are in Account.</p>
              </div>
            </li>
          </ol>
          <p className="aside">To serve the UI over HTTPS on your own domain, set <code>KIPITINY_DOMAIN</code> and <code>KIPITINY_ACME_EMAIL</code>. Everything else is in the <Link to="/docs">documentation</Link>.</p>
        </section>

      </main>
      <Footer />
    </div>
  );
}
