import { Link } from "react-router-dom";
import {
  ArrowRight,
  BarChart3,
  Coins,
  Github,
  Radio,
  Route as RouteIcon,
  ShieldCheck,
  Wrench,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { FeatureCard, Section, SectionHeading, Stat } from "@/components/marketing/sections";
import { usePageMeta } from "@/components/marketing/head";
import { site } from "@/site.config";

/** Eight steps, in the order the control plane runs them on every call. */
const pipeline = [
  {
    n: "1",
    t: "Authorise",
    d: "Source address matched to a customer, with concurrent call and CPS limits applied.",
  },
  {
    n: "2",
    t: "Normalise",
    d: "The dialled number is turned into E.164 using the customer's tech prefix and country code.",
  },
  { n: "3", t: "Rate", d: "The selling rate is found by longest prefix, converted to the account currency." },
  {
    n: "4",
    t: "Reserve",
    d: "Money for the first minutes is pre-deducted in one transaction before the call is tried.",
  },
  {
    n: "5",
    t: "Route",
    d: "Carriers are ordered by cost, quality or share, and endpoints are expanded for failover.",
  },
  {
    n: "6",
    t: "Bridge",
    d: "FreeSWITCH dials each attempt in turn, applying codec, media and header policy.",
  },
  {
    n: "7",
    t: "Bill",
    d: "On hangup the call is billed to the increment and the unused reservation is released.",
  },
  {
    n: "8",
    t: "Record",
    d: "A CDR is written with the rate, cost, margin, area, operator and every attempt.",
  },
];

export function MarketingHomePage() {
  usePageMeta(
    "Session border controller with prepaid billing",
    "SIP security, least cost routing and per-second prepaid billing in one system you run yourself, built on FreeSWITCH, Go and PostgreSQL.",
  );
  return (
    <>
      {/* Hero */}
      <section className="relative overflow-hidden border-b">
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 bg-[radial-gradient(60%_60%_at_50%_0%,color-mix(in_oklch,var(--primary)_18%,transparent),transparent)]"
        />
        <div className="relative mx-auto w-full max-w-6xl px-4 py-20 sm:px-6 sm:py-28">
          <div className="max-w-3xl">
            <span className="inline-flex items-center gap-2 rounded-full border bg-background px-3 py-1 text-xs text-muted-foreground">
              <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
              Running in production on wholesale traffic
            </span>
            <h1 className="mt-5 text-balance text-4xl font-semibold leading-[1.1] tracking-tight sm:text-5xl lg:text-6xl">
              The session border controller that also keeps the books.
            </h1>
            <p className="mt-6 max-w-2xl text-pretty text-lg leading-relaxed text-muted-foreground">
              {site.summary}
            </p>
            <div className="mt-8 flex flex-wrap items-center gap-3">
              <Button asChild size="lg">
                <Link to={site.portalPath} data-testid="hero-login">
                  Open the operator portal <ArrowRight />
                </Link>
              </Button>
              <Button asChild size="lg" variant="outline">
                <Link to="/features">See what it does</Link>
              </Button>
            </div>
            <dl className="mt-12 grid gap-4 sm:grid-cols-4">
              <Stat value="8 steps" label="Every call runs the same decision pipeline, in order" />
              <Stat value="6 decimals" label="Money is exact: no floating point anywhere in billing" />
              <Stat value="4 modes" label="Least cost, lossless, quality weighted or percentage split" />
              <Stat value="30 days" label="Every SIP message captured and replayable as a pcap" />
            </dl>
          </div>
        </div>
      </section>

      {/* What it is */}
      <Section>
        <SectionHeading
          eyebrow="What it is"
          title="One box between your customers and your carriers"
          lead="Most operators bolt a softswitch to a billing system and spend their nights reconciling the two. SuperSBC decides and bills in the same transaction, so a call that connects is a call that is paid for."
        />
        <div className="mt-12 grid gap-6 md:grid-cols-2 lg:grid-cols-3">
          <FeatureCard
            icon={Radio}
            title="SIP and media"
            points={[
              "UDP, TCP and TLS",
              "SRTP anchored or bypassed",
              "Transcoding and DTMF interworking",
              "Topology hiding",
            ]}
          >
            FreeSWITCH carries the signalling and the media, with the SBC deciding what each leg is allowed to
            negotiate.
          </FeatureCard>
          <FeatureCard
            icon={RouteIcon}
            title="Routing that pays"
            points={[
              "Least cost with ordered failover",
              "Lossless: never route below your sell rate",
              "Quality weighted by live ASR",
              "Percentage split and time of day",
            ]}
          >
            Carriers are picked per call from the route group, expanded into endpoints, and retried in order
            when one fails.
          </FeatureCard>
          <FeatureCard
            icon={Coins}
            title="Prepaid billing"
            points={[
              "Money reserved before the call",
              "Per second or any increment",
              "Multi currency with FX",
              "Invoices, payments and a ledger",
            ]}
          >
            Balance is pre-deducted at setup and settled on hangup, so a customer cannot spend money they do
            not have.
          </FeatureCard>
          <FeatureCard
            icon={BarChart3}
            title="CDRs and reporting"
            points={[
              "Rate, cost and margin on every call",
              "Area and operator labels",
              "Why a rejected call was rejected",
              "CSV per customer, carrier or full",
            ]}
          >
            Every attempt is recorded with what it cost, what it sold for and which carrier carried it.
          </FeatureCard>
          <FeatureCard
            icon={ShieldCheck}
            title="Security by default"
            points={[
              "Firewall synced from the database",
              "Automatic bans for scanners",
              "TLS, SRTP and STIR/SHAKEN",
              "Roles, second factor and audit log",
            ]}
          >
            Signalling is only accepted from addresses you listed, and anything else is dropped by the kernel.
          </FeatureCard>
          <FeatureCard
            icon={Wrench}
            title="Operable at 3am"
            points={[
              "Live calls and SIP trace",
              "Readiness and Prometheus metrics",
              "Watchdog e-mails on anything abnormal",
              "One command deploys",
            ]}
          >
            When a call fails you can see the exact SIP exchange, the routing decision and the reason in one
            place.
          </FeatureCard>
        </div>
      </Section>

      {/* Pipeline */}
      <Section muted>
        <SectionHeading
          eyebrow="How a call flows"
          title="The same eight decisions, every time"
          lead="Nothing is implicit. Each step either passes the call on with a decision attached, or stops it with a reason an operator can act on."
        />
        <ol className="mt-12 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {pipeline.map((s) => (
            <li key={s.n} className="relative rounded-xl border bg-card p-5">
              <span className="text-xs font-semibold text-primary">Step {s.n}</span>
              <h3 className="mt-1 font-semibold">{s.t}</h3>
              <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{s.d}</p>
            </li>
          ))}
        </ol>
      </Section>

      {/* Stack */}
      <Section>
        <div className="grid gap-12 lg:grid-cols-[1fr_1.1fr] lg:items-center">
          <div className="min-w-0">
            <SectionHeading
              eyebrow="Built on what you already trust"
              title="Boring technology, deliberately"
              lead="Nothing here is exotic. Every piece is something a telecoms engineer can already debug at three in the morning, and the whole stack runs on one machine or several."
            />
            <div className="mt-8 flex flex-wrap gap-2">
              {site.stack.map((s) => (
                <span key={s.name} className="rounded-full border bg-card px-3 py-1.5 text-sm">
                  <span className="font-medium">{s.name}</span>
                  <span className="text-muted-foreground"> · {s.role}</span>
                </span>
              ))}
            </div>
            <div className="mt-8 flex flex-wrap gap-3">
              <Button asChild variant="outline">
                <a href={site.repo} target="_blank" rel="noreferrer">
                  <Github /> Source and documentation
                </a>
              </Button>
              <Button asChild variant="ghost">
                <Link to="/contact">
                  Talk to us <ArrowRight />
                </Link>
              </Button>
            </div>
          </div>
          <div className="min-w-0 rounded-xl border bg-card p-6">
            <h3 className="text-sm font-semibold">What you run</h3>
            <p className="mt-2 text-sm text-muted-foreground">
              One repository, two stacks: a development stack with mock carriers for testing changes, and a
              live stack on the host network carrying real traffic. The same commands drive both.
            </p>
            <pre className="mt-5 overflow-x-auto rounded-lg bg-muted p-4 text-xs leading-relaxed">
              <code>{`make live-up         # build and start the live stack
make live-firewall   # apply the nftables ruleset
make live-tls        # issue and install certificates
make live-watchdog   # arm the service watchdog
make live-reconcile  # check every balance against the ledger`}</code>
            </pre>
          </div>
        </div>
      </Section>

      {/* CTA */}
      <Section muted>
        <div className="rounded-2xl border bg-card px-6 py-12 text-center sm:px-12">
          <h2 className="text-balance text-3xl font-semibold tracking-tight">Ready to look inside?</h2>
          <p className="mx-auto mt-4 max-w-xl text-pretty text-muted-foreground">
            The operator portal is where routing, rates, customers, carriers and every call record live. If
            you have an account, it is one click away.
          </p>
          <div className="mt-8 flex flex-wrap justify-center gap-3">
            <Button asChild size="lg">
              <Link to={site.portalPath}>
                Log in to the portal <ArrowRight />
              </Link>
            </Button>
            <Button asChild size="lg" variant="outline">
              <Link to="/contact">Request access</Link>
            </Button>
          </div>
        </div>
      </Section>
    </>
  );
}
