import { Link } from "react-router-dom";
import {
  ArrowRight,
  BarChart3,
  Building2,
  Coins,
  FileText,
  Globe,
  Radio,
  Route as RouteIcon,
  Settings,
  ShieldCheck,
  Table2,
  Users,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Section, SectionHeading } from "@/components/marketing/sections";
import { usePageMeta } from "@/components/marketing/head";
import { site } from "@/site.config";

interface Capability {
  icon: LucideIcon;
  title: string;
  lead: string;
  items: { name: string; detail: string }[];
}

/** Everything the product actually does, grouped the way an operator thinks
 * about it. Each entry matches a screen or a behaviour that exists. */
const capabilities: Capability[] = [
  {
    icon: Radio,
    title: "SIP and media",
    lead: "FreeSWITCH terminates both legs, so the carrier never sees your customer and the customer never sees your carrier.",
    items: [
      {
        name: "Transports",
        detail:
          "UDP, TCP and TLS on separate ingress and egress profiles, with their own ports and certificates.",
      },
      {
        name: "Media",
        detail:
          "Anchor the RTP or let it flow end to end, per customer and per carrier, with SRTP optional or mandatory.",
      },
      {
        name: "Codecs",
        detail:
          "An allowed list per customer and per carrier; the intersection is what gets offered, transcoding when it has to.",
      },
      { name: "DTMF", detail: "RFC2833, inband and SIP INFO interworked between legs." },
      {
        name: "Topology hiding",
        detail: "Headers rewritten on the way through, with rules you can add per customer or carrier.",
      },
      {
        name: "Early media",
        detail: "Progress and ringback handled per carrier, including carriers that lie about it.",
      },
    ],
  },
  {
    icon: RouteIcon,
    title: "Routing",
    lead: "A route group holds prefixes; each prefix holds carriers; each carrier holds endpoints. The call walks down that tree until something answers.",
    items: [
      {
        name: "Least cost",
        detail: "Carriers ordered by their buy rate for the dialled prefix, cheapest first.",
      },
      {
        name: "Lossless",
        detail: "Any carrier whose cost is above your selling rate is skipped, so a call never loses money.",
      },
      {
        name: "Quality weighted",
        detail:
          "Live ASR per carrier and prefix decides the order, so a carrier that stops completing drops down by itself.",
      },
      {
        name: "Percentage split",
        detail: "Send an agreed share of traffic to each carrier, for commitments and ratio deals.",
      },
      {
        name: "Failover",
        detail:
          "Ordered attempts with a configurable list of SIP codes that mean try the next one, and a cap on how far to walk.",
      },
      {
        name: "Endpoints",
        detail:
          "Several addresses per carrier in failover, round robin or weighted mode, with dead endpoints parked and revived.",
      },
      {
        name: "Time of day",
        detail: "A routing window per carrier, so a route is only used in the hours you agreed.",
      },
      {
        name: "Simulator",
        detail:
          "Type a number and a customer and see the exact decision, including every carrier that was skipped and why.",
      },
    ],
  },
  {
    icon: Coins,
    title: "Prepaid billing",
    lead: "Money is reserved before the call is tried and settled when it hangs up, in the same transaction as the CDR.",
    items: [
      {
        name: "Reservations",
        detail:
          "The first minutes are pre-deducted at the selling rate; a customer cannot spend a balance that is not there.",
      },
      {
        name: "Exact money",
        detail:
          "Every amount is a decimal with six places, in the database and in the code. No floating point, ever.",
      },
      {
        name: "Increments",
        detail: "Per second, 60/60, 30/6 or anything else, with connect fee and minimum duration per rate.",
      },
      {
        name: "Multi currency",
        detail:
          "Decks in one currency, accounts in another, converted at a stored FX rate recorded on the CDR.",
      },
      {
        name: "Credit and alerts",
        detail: "A credit limit per customer and a low balance notification before calls start failing.",
      },
      {
        name: "Invoices",
        detail:
          "Period or custom range invoices with payments, allocations and a running ledger behind them.",
      },
      {
        name: "Reconciliation",
        detail: "One command re-derives every balance from the ledger and reports any mismatch.",
      },
    ],
  },
  {
    icon: Table2,
    title: "Call records and reporting",
    lead: "One row per attempt, with everything needed to answer a customer dispute without opening a packet capture.",
    items: [
      {
        name: "Full CDR",
        detail:
          "Caller, called, both raw and normalised, carrier, codecs, transport, PDD, duration, billed seconds.",
      },
      {
        name: "Money per call",
        detail: "Selling rate, cost, margin, currency, FX and the exact reservation and charge.",
      },
      {
        name: "Area and operator",
        detail:
          "The dialled number labelled with its area and operator from a maintained table, written when the CDR is created.",
      },
      {
        name: "Why it failed",
        detail:
          "A rejected call carries the step it stopped at and what to change, down to the two rates behind a lossless skip.",
      },
      {
        name: "Exports",
        detail:
          "CSV in a customer view, a carrier view or the full view, with the columns each side is allowed to see.",
      },
      {
        name: "Reports",
        detail:
          "ASR, NER, ACD, PDD, minutes, revenue, cost and margin by customer, carrier, destination or hour.",
      },
    ],
  },
  {
    icon: Users,
    title: "Customers",
    lead: "A customer is a set of addresses, a rate deck, a route group and the limits you are willing to give them.",
    items: [
      {
        name: "IP authentication",
        detail: "Several addresses or ranges per customer, each with its own port and transport.",
      },
      {
        name: "Capacity",
        detail: "Concurrent call and calls per second ceilings, enforced before anything else happens.",
      },
      {
        name: "Number handling",
        detail: "Tech prefix, default country code, international prefix and caller ID policy per customer.",
      },
      {
        name: "Policy",
        detail:
          "Require TLS, require SRTP, trust P-Asserted-Identity, privacy handling, blocked destination prefixes.",
      },
      {
        name: "Accounts",
        detail: "Balance, credit, currency, invoices and the full ledger behind every movement.",
      },
    ],
  },
  {
    icon: Building2,
    title: "Carriers",
    lead: "A carrier is one or more endpoints, a buying deck and a quality record that routing actually uses.",
    items: [
      {
        name: "Endpoints",
        detail:
          "Multiple hosts per carrier with priority and weight, a failure threshold and automatic revival.",
      },
      {
        name: "Health",
        detail:
          "OPTIONS ping with live gateway state, and a breaker that marks a carrier degraded on consecutive faults or a collapsing ASR.",
      },
      {
        name: "Buying decks",
        detail:
          "Rate import with effective dates, so tomorrow's prices load today without touching live routing.",
      },
      {
        name: "Identity",
        detail: "ANI prefix, tech prefix, codec list, media mode and failover codes per carrier.",
      },
    ],
  },
  {
    icon: FileText,
    title: "Rates and area codes",
    lead: "Decks and number intelligence are data you maintain, not code someone has to deploy.",
    items: [
      {
        name: "Rate decks",
        detail: "CSV import with longest prefix matching, effective dates and per prefix increments.",
      },
      {
        name: "Area codes",
        detail:
          "Country code plus area code to a description and operator, shipped with the DRC and all 2,598 Indian STD codes.",
      },
      {
        name: "Blocked prefixes",
        detail:
          "A global list and a per customer list, for premium ranges and destinations you will not carry.",
      },
      {
        name: "Number normalisation",
        detail: "Anything a customer sends is turned into E.164 before it is rated or routed.",
      },
    ],
  },
  {
    icon: ShieldCheck,
    title: "Security",
    lead: "The firewall, the application and the database agree on who is allowed to send you traffic.",
    items: [
      {
        name: "Kernel level filtering",
        detail:
          "An nftables ruleset where SIP is only accepted from customer and carrier addresses, kept in sync with the database automatically.",
      },
      {
        name: "Scanner protection",
        detail:
          "Unauthorised INVITEs and failed logins are counted per address and banned, with customers and your own addresses never banned.",
      },
      {
        name: "Rate limits",
        detail: "Per source packet meters on SIP, and per address request limits on the portal and the API.",
      },
      {
        name: "Transport security",
        detail: "TLS for SIP and the portal with certificates renewed automatically, SRTP for media.",
      },
      {
        name: "STIR/SHAKEN",
        detail: "Identity verification with per customer policy, attestation recorded on the CDR.",
      },
      {
        name: "Portal access",
        detail:
          "Roles, time based second factor, API keys, session control and an audit log of every change.",
      },
    ],
  },
  {
    icon: BarChart3,
    title: "Troubleshooting",
    lead: "Every question an operator asks at three in the morning has a screen that answers it.",
    items: [
      {
        name: "Live calls",
        detail: "What is up right now, per customer and carrier, with the ability to hang a call up.",
      },
      {
        name: "SIP trace",
        detail:
          "The full exchange for a call from the capture store, readable in the browser and downloadable as a pcap.",
      },
      {
        name: "Attempt history",
        detail: "Every carrier tried for a call, with the code each returned and how long it took.",
      },
      { name: "Audit log", detail: "Who changed what, when, and what the value was before." },
    ],
  },
  {
    icon: Settings,
    title: "Operations",
    lead: "The parts that decide whether a system survives its second year.",
    items: [
      {
        name: "Health and metrics",
        detail:
          "A readiness endpoint per dependency and Prometheus metrics for calls, rejections, ASR, PDD, bans and gateways.",
      },
      {
        name: "Watchdog",
        detail:
          "Checks every service every five minutes and e-mails an alert the moment anything is abnormal, with a daily all clear.",
      },
      { name: "Dashboards", detail: "Grafana dashboards ready to turn on, fed by the same metrics." },
      {
        name: "Backups and migrations",
        detail: "Scheduled database backups and ordered migrations applied on start.",
      },
      {
        name: "REST API",
        detail: "Everything the portal does is an API call, documented with OpenAPI, usable with an API key.",
      },
    ],
  },
];

export function MarketingFeaturesPage() {
  usePageMeta(
    "Capabilities",
    "Routing modes, prepaid billing, call records, customers, carriers, rate decks, security and operations: everything SuperSBC does.",
  );
  return (
    <>
      <Section>
        <SectionHeading
          eyebrow="Capabilities"
          title="Everything it does, in the operator's own order"
          lead="This is the full list, not a highlight reel. If something is on this page it is on a screen in the portal or an endpoint in the API."
        />
        <nav className="mt-10 flex flex-wrap gap-2">
          {capabilities.map((c) => (
            <a
              key={c.title}
              href={`#${slug(c.title)}`}
              className="rounded-full border bg-card px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
            >
              {c.title}
            </a>
          ))}
        </nav>
      </Section>

      {capabilities.map((c, i) => (
        <Section key={c.title} muted={i % 2 === 1}>
          <div id={slug(c.title)} className="scroll-mt-20">
            <div className="flex items-start gap-4">
              <span className="grid h-11 w-11 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary">
                <c.icon className="h-5 w-5" />
              </span>
              <div className="max-w-2xl">
                <h2 className="text-2xl font-semibold tracking-tight">{c.title}</h2>
                <p className="mt-2 text-pretty leading-relaxed text-muted-foreground">{c.lead}</p>
              </div>
            </div>
            <dl className="mt-8 grid gap-x-10 gap-y-6 sm:grid-cols-2">
              {c.items.map((item) => (
                <div key={item.name} className="border-l-2 border-primary/25 pl-4">
                  <dt className="font-medium">{item.name}</dt>
                  <dd className="mt-1 text-sm leading-relaxed text-muted-foreground">{item.detail}</dd>
                </div>
              ))}
            </dl>
          </div>
        </Section>
      ))}

      <Section>
        <div className="flex flex-col items-center gap-6 text-center">
          <Globe className="h-8 w-8 text-primary" />
          <SectionHeading
            center
            title="Want to see it against your own traffic?"
            lead="The quickest way to judge an SBC is to point a test customer at it and watch the CDRs. Get in touch and we will set that up."
          />
          <div className="flex flex-wrap justify-center gap-3">
            <Button asChild size="lg">
              <Link to="/contact">
                Contact us <ArrowRight />
              </Link>
            </Button>
            <Button asChild size="lg" variant="outline">
              <Link to={site.portalPath}>Log in</Link>
            </Button>
          </div>
        </div>
      </Section>
    </>
  );
}

function slug(s: string) {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, "-");
}
