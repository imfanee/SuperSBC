import { Link } from "react-router-dom";
import { ArrowRight, Ban, Eye, FileCheck2, KeyRound, Lock, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { FeatureCard, Section, SectionHeading } from "@/components/marketing/sections";
import { usePageMeta } from "@/components/marketing/head";
import { site } from "@/site.config";

/** The layers a packet passes through, outermost first. */
const layers = [
  {
    name: "The kernel, before anything else",
    detail:
      "An nftables ruleset drops everything by default. SIP is accepted only from the addresses of your customers and carriers, kept in sync with the database by a service that reacts within seconds of a change. RTP has its own range, and everything else, the control API, the database, the cache, is reachable only from inside.",
  },
  {
    name: "Rate limits in front of the ports",
    detail:
      "Per source packet meters sit on SIP so a flood is dropped by the kernel before FreeSWITCH parses it. The portal and the API have their own per address limits, strictest on the doors worth guessing at: sign in, password reset and second factor.",
  },
  {
    name: "Automatic bans",
    detail:
      "Unauthorised INVITEs and failed sign ins are counted per address. Cross the threshold and the address joins the ban list the firewall drops. Customer addresses, carrier addresses and the node's own addresses are never banned automatically, because that would take their calls down with them; they are raised for an operator instead.",
  },
  {
    name: "Authenticated and encrypted transport",
    detail:
      "SIP over TLS and SRTP for media, with certificates issued and renewed automatically and the same certificate presented by the portal, the SIP stack and the WebSocket endpoint.",
  },
  {
    name: "Identity",
    detail:
      "STIR/SHAKEN verification with a policy per customer: ignore it, record it, or refuse the call. The attestation level is written to the CDR either way.",
  },
  {
    name: "Inside the portal",
    detail:
      "Roles separate who can look from who can change, a time based second factor protects the accounts that can, API keys are hashed, sessions are short, and every change lands in an audit log with the value it replaced.",
  },
];

export function MarketingSecurityPage() {
  usePageMeta(
    "Security",
    "Kernel level filtering, automatic bans, TLS and SRTP, STIR/SHAKEN, prepaid limits and audit: how SuperSBC keeps scanners and toll fraud out.",
  );
  return (
    <>
      <Section>
        <SectionHeading
          eyebrow="Security"
          title="A scanner finds a new SIP port within hours"
          lead="That is the baseline assumption. Everything below exists because an SBC on a public address is probed continuously, and because the cheapest fraud is the one nobody notices until the carrier invoice arrives."
        />
        <div className="mt-12 grid gap-6 md:grid-cols-3">
          <FeatureCard icon={ShieldCheck} title="Closed by default">
            Nothing reaches the signalling stack unless you listed the address. The firewall, the application
            and the database agree on that list at all times.
          </FeatureCard>
          <FeatureCard icon={Ban} title="Expensive to probe">
            Floods are dropped in the kernel, scanners are banned automatically, and known scanner paths on
            the portal close the connection without answering.
          </FeatureCard>
          <FeatureCard icon={Lock} title="Encrypted end to end">
            TLS for signalling and the portal, SRTP for media, certificates renewed without stopping the
            service.
          </FeatureCard>
        </div>
      </Section>

      <Section muted>
        <SectionHeading eyebrow="Defence in depth" title="What a packet passes through" />
        <ol className="mt-10 space-y-4">
          {layers.map((l, i) => (
            <li key={l.name} className="flex gap-5 rounded-xl border bg-card p-6">
              <span className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-primary text-sm font-semibold text-primary-foreground">
                {i + 1}
              </span>
              <div>
                <h3 className="font-semibold">{l.name}</h3>
                <p className="mt-2 text-pretty text-sm leading-relaxed text-muted-foreground">{l.detail}</p>
              </div>
            </li>
          ))}
        </ol>
      </Section>

      <Section>
        <SectionHeading
          eyebrow="Fraud and money"
          title="The controls that stop a bad night becoming a bad quarter"
          lead="Toll fraud is a billing problem before it is a security problem. These are the limits that cap the damage even when credentials are correct."
        />
        <div className="mt-10 grid gap-6 md:grid-cols-2 lg:grid-cols-4">
          <FeatureCard icon={KeyRound} title="Prepaid by design">
            Money is reserved before a call is attempted, so a compromised customer can only ever spend the
            balance they have.
          </FeatureCard>
          <FeatureCard icon={Ban} title="Blocked destinations">
            A global blacklist and a per customer list keep premium and satellite ranges off your network
            unless you opt in.
          </FeatureCard>
          <FeatureCard icon={Eye} title="Capacity ceilings">
            Concurrent call and calls per second limits per customer, plus a global cap for the node, applied
            before rating.
          </FeatureCard>
          <FeatureCard icon={FileCheck2} title="Everything recorded">
            Each attempt leaves a CDR with cost and margin, and each rejection says which step stopped it and
            why.
          </FeatureCard>
        </div>
      </Section>

      <Section muted>
        <div className="rounded-2xl border bg-card px-6 py-12 text-center sm:px-12">
          <h2 className="text-balance text-2xl font-semibold tracking-tight sm:text-3xl">
            Questions about a specific control?
          </h2>
          <p className="mx-auto mt-3 max-w-xl text-pretty text-muted-foreground">
            Every decision in the system is written down, including the ones that were deliberately not taken.
            Ask and we will show you the exact behaviour.
          </p>
          <div className="mt-8 flex flex-wrap justify-center gap-3">
            <Button asChild size="lg">
              <Link to="/contact">
                Ask us <ArrowRight />
              </Link>
            </Button>
            <Button asChild size="lg" variant="outline">
              <a href={site.repo} target="_blank" rel="noreferrer">
                Read the decisions
              </a>
            </Button>
          </div>
        </div>
      </Section>
    </>
  );
}
