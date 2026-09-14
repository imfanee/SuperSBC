import { expect, test } from "@playwright/test";
import { login, unique } from "./helpers";

test.describe.configure({ mode: "serial" });

test("login rejects a wrong password and accepts the admin", async ({ page }) => {
  await page.goto("/login");
  await page.getByTestId("email").fill("admin@example.com");
  await page.getByTestId("password").fill("definitely-wrong");
  await page.getByTestId("login-submit").click();
  await expect(page.getByText("invalid credentials")).toBeVisible();
  await login(page);
});

test("create a customer with an IP, import a rate deck, build a route, simulate, see the CDR", async ({ page }) => {
  await login(page);

  // rate group with a CSV import (validate, then import)
  const rgName = unique("ui-rates");
  await page.goto("/rate-groups");
  await page.getByTestId("new-rate-group").click();
  await page.getByTestId("rate-group-name").fill(rgName);
  await page.getByTestId("rate-group-submit").click();
  await expect(page.getByRole("heading", { name: rgName })).toBeVisible();
  await page.getByTestId("import-open").click();
  await page.getByTestId("import-file").setInputFiles({
    name: "rates.csv",
    mimeType: "text/csv",
    buffer: Buffer.from("prefix,destination,rate_per_min,connect_fee,initial_increment,subsequent_increment\n44,UK Fixed,0.0100,0,60,60\n447,UK Mobile,0.0200,0,60,60\n4477,UK O2,0.0190,0,30,6\n"),
  });
  await page.getByTestId("import-validate").click();
  await expect(page.getByText("3 valid")).toBeVisible();
  await page.getByTestId("import-run").click();
  await expect(page.getByText("Imported 3 rates")).toBeVisible();
  await page.getByTestId("test-number").fill("447700900123");
  await page.getByTestId("test-submit").click();
  await expect(page.getByTestId("test-prefix")).toHaveText("4477");

  // route group with one route to carrier-answer
  const routeGroup = unique("ui-routes");
  await page.goto("/route-groups");
  await page.getByTestId("new-route-group").click();
  await page.getByTestId("route-group-name").fill(routeGroup);
  await page.getByTestId("route-group-submit").click();
  await expect(page.getByRole("heading", { name: routeGroup })).toBeVisible();
  await page.getByTestId("new-route").click();
  await page.getByTestId("route-prefix").fill("44");
  await page.getByTestId("route-destination").fill("UK via answer");
  await page.getByTestId("route-add-carrier").click();
  await page.getByRole("option", { name: /carrier-answer/ }).click();
  await page.getByTestId("route-add-carrier-btn").click();
  await expect(page.getByTestId("route-carrier-0")).toContainText("carrier-answer");
  await page.getByTestId("route-save").click();
  await expect(page.getByText("Route saved")).toBeVisible();

  // customer using both, with an IP
  const customer = unique("ui-cust");
  await page.goto("/customers");
  await page.getByTestId("new-customer").click();
  await page.getByTestId("customer-name").fill(customer);
  await page.getByTestId("customer-rate-group").click();
  await page.getByRole("option", { name: new RegExp(rgName) }).click();
  await page.getByTestId("customer-route-group").click();
  await page.getByRole("option", { name: routeGroup }).click();
  await page.getByTestId("customer-submit").click();
  await expect(page.getByRole("heading", { name: customer })).toBeVisible();
  await page.getByRole("tab", { name: "IPs" }).click();
  await page.getByTestId("ip-cidr").fill("192.0.2.77");
  await page.getByTestId("ip-add").click();
  await expect(page.getByText("192.0.2.77/32")).toBeVisible();
  await page.getByRole("tab", { name: "Account" }).click();
  await page.getByTestId("topup-amount").fill("25.00");
  await page.getByTestId("topup-submit").click();
  await expect(page.getByText("Posted")).toBeVisible();

  // simulate a call
  await page.goto("/simulator");
  await page.getByTestId("sim-customer").click();
  await page.getByRole("option", { name: customer }).click();
  await page.getByTestId("sim-number").fill("00447700900123");
  await page.getByTestId("sim-submit").click();
  await expect(page.getByTestId("sim-result")).toContainText("200 OK");
  await expect(page.getByText("carrier-answer").first()).toBeVisible();

  // CDRs: filter to answered calls placed by the Go e2e suite and open one
  await page.goto("/cdrs?disposition=answered&from=2020-01-01T00:00");
  const firstRow = page.locator("tbody tr").first();
  await expect(firstRow).toBeVisible();
  await firstRow.click();
  await expect(page.getByText("Attempts timeline")).toBeVisible();
});

test("dashboard, live calls, reports and system pages render", async ({ page }) => {
  await login(page);
  await expect(page.getByText("Calls per hour")).toBeVisible();
  await page.goto("/calls");
  await expect(page.getByRole("heading", { name: "Live calls" })).toBeVisible();
  await page.goto("/reports");
  await expect(page.getByText("Daily traffic profile")).toBeVisible();
  await page.goto("/system");
  await expect(page.getByText("Dependencies")).toBeVisible();
  await page.getByRole("tab", { name: "Failover rules" }).click();
  await expect(page.getByText("Number fault (stop, relay to customer)")).toBeVisible();
  // command palette
  await page.keyboard.press("Control+k");
  await page.getByPlaceholder("Type a page name...").fill("carriers");
  await page.getByRole("option", { name: /Carriers/ }).first().click();
  await expect(page.getByRole("heading", { name: "Carriers" })).toBeVisible();
});
