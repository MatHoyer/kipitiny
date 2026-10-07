import { Link, NavLink } from "react-router";
import { GITHUB, VERSION } from "~/lib/site";

export function Logo() {
  return (
    <svg viewBox="0 0 26 26" aria-hidden="true">
      <rect x="1" y="1" width="24" height="24" rx="5" fill="none" stroke="currentColor" strokeWidth="2" />
      <rect x="6" y="15" width="5" height="5" rx="1" fill="#f5c518" />
    </svg>
  );
}

export function Header() {
  return (
    <header className="top">
      <Link className="mark" to="/" aria-label="kipitiny home">
        <Logo />
        kipitiny
      </Link>
      <nav>
        <a className="hide-sm" href="/#backups">
          Backups
        </a>
        <a className="hide-sm" href="/#install">
          Install
        </a>
        <NavLink to="/docs">Docs</NavLink>
        <a href={GITHUB}>GitHub</a>
      </nav>
    </header>
  );
}

export function Footer() {
  return (
    <footer>
      <span>{VERSION ? `kipitiny ${VERSION}` : "kipitiny"}</span>
      <nav>
        <Link to="/docs">Docs</Link>
        <a href={GITHUB}>GitHub</a>
        <a href={`${GITHUB}/releases`}>Releases</a>
        <a href="/llms.txt">llms.txt</a>
      </nav>
    </footer>
  );
}
