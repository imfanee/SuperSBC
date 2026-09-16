/**
 * Page footer with the author credit. It is the last flex child of the
 * scrolling column, so it sits after the content (never fixed, never
 * overlapping) and only appears when the page is scrolled to its end or the
 * content is shorter than the viewport.
 */
export function CreditFooter() {
  return (
    <footer className="mt-auto border-t px-4 py-3 text-center md:px-6" role="contentinfo">
      <p className="font-lcd text-sm leading-tight tracking-wide text-muted-foreground sm:text-base">
        Architected and Developed by:- Faisal Hanif |{" "}
        <a
          href="mailto:imfanee@gmail.com"
          className="underline-offset-2 hover:text-foreground hover:underline"
        >
          imfanee@gmail.com
        </a>
      </p>
    </footer>
  );
}
