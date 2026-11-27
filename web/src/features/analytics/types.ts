export type AnalyticsSettings = {
  enabled: boolean;
  /** Serves the tracker script and receives hits. */
  domain: string;
  retentionDays: number;
};

/** Steps of turning analytics on, as the manager reports them. */
export type AnalyticsStep = "image" | "start" | "health" | "sites";

export type AnalyticsRow = { value: string; count: number; visitors: number };

export type AnalyticsSummary = {
  pageviews: number;
  visitors: number;
  visits: number;
  bounces: number;
  /** Summed length of the visits, in seconds. */
  duration: number;
};

export type AnalyticsStats = AnalyticsSummary & {
  /** The same span just before. */
  previous: AnalyticsSummary;
  /** Visitors of the last 30 minutes. */
  active: number;
  series: { t: number; pageviews: number; visitors: number }[];
  breakdowns: Record<string, AnalyticsRow[]>;
  /** The tag the app's pages include. */
  script: string;
};

export type AnalyticsVisit = {
  id: string;
  startedAt: number;
  lastAt: number;
  pageviews: number;
  /** Custom events. */
  events: number;
  entry: string;
  exit: string;
  referrer: string;
  utmSource: string;
  utmMedium: string;
  utmCampaign: string;
  country: string;
  browser: string;
  os: string;
  device: string;
  screen: string;
  language: string;
  /** What the app passed to kipitiny.identify. */
  identity: string;
  identityProps?: Record<string, unknown>;
};

export type AnalyticsVisitDetail = AnalyticsVisit & {
  /** Pageviews (name empty) and custom events, in order. */
  journey: { at: number; name: string; path: string; query: string; title: string; props?: Record<string, unknown> }[];
};

export type TrackerKind = "pixel" | "link";

/** A pixel (1x1 GIF counting views) or a tracked link (redirect counting clicks). */
export type Tracker = {
  id: string;
  kind: TrackerKind;
  /** The URL's last part. */
  slug: string;
  name: string;
  /** A link's destination. */
  target?: string;
  createdAt: number;
  /** Views or clicks of the last 30 days. */
  hits: number;
  url: string;
};

export type TrackerInput = { kind: TrackerKind; name: string; slug: string; target?: string };

/** What a shared analytics link shows. */
export type SharedAnalytics = Omit<AnalyticsStats, "script"> & { domain: string };

export type TrackerStats = Omit<AnalyticsStats, "script"> & { tracker: Tracker };

export type IssueStatus = "open" | "resolved" | "ignored";

/** The events of one fingerprint; times are unix seconds. */
export type Issue = {
  id: number;
  title: string;
  /** Where it was thrown: function · file:line. */
  culprit: string;
  level: string;
  platform: string;
  status: IssueStatus;
  firstSeen: number;
  lastSeen: number;
  events: number;
  users: number;
  firstRelease: string;
  lastRelease: string;
  resolvedAt?: number;
  /** When a resolved issue happened again. */
  regressedAt?: number;
  /** Events per hour of the last 24, oldest first (lists only). */
  spark?: number[];
};

export type ErrorsView = { issues: Issue[]; counts: Record<IssueStatus, number>; dsn: string };

export type ErrorFrame = {
  function?: string;
  module?: string;
  file?: string;
  line?: number;
  col?: number;
  inApp?: boolean;
  /** Source around line, which is context[contextLine]. */
  context?: string[];
  contextLine?: number;
};

export type ErrorException = {
  type?: string;
  value?: string;
  module?: string;
  mechanism?: string;
  handled?: boolean;
  /** Oldest first: the last one threw. */
  frames?: ErrorFrame[];
};

export type ErrorEvent = {
  id: string;
  at: number;
  level?: string;
  platform?: string;
  release?: string;
  environment?: string;
  serverName?: string;
  transaction?: string;
  message?: string;
  /** The last one is the one raised. */
  exceptions?: ErrorException[];
  breadcrumbs?: { at?: number; type?: string; category?: string; level?: string; message?: string; data?: Record<string, string> }[];
  tags?: Record<string, string>;
  contexts?: Record<string, string>;
  user?: string;
  url?: string;
  method?: string;
  country?: string;
  sdk?: string;
};

/** The current deployment's errors over its first hour, against the release before over the day before; rates per minute. */
export type DeployErrors = {
  deploymentId: string;
  release: string;
  minutes: number;
  events: number;
  newIssues: number;
  previous?: string;
  previousRelease?: string;
  rate: number;
  previousRate: number;
  spike: boolean;
};

/** One kept event of an issue; older and newer are its neighbours' ids. */
export type IssueEvent = { event: ErrorEvent; older?: string; newer?: string; index: number; kept: number };
