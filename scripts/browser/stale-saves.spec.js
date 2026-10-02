import { test, expect } from './event-fixture.js';

// Two real clients paired with a real server. Client B corrects a record
// while client A still shows (or has queued) an older version of it: A's
// change must not overwrite B's, A's volunteer is told, and every other
// change goes through. Nothing is mocked.

const url = (client, path) => new URL(path, client.baseURL).href;

async function post(request, client, path, data) {
  const response = await request.post(url(client, path), { data });
  expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
  return response.json();
}

async function seed(request, event) {
  await post(request, event.a, '/api/prefixes', [{ prefix: 'A', color: 'green', weight: 1 }]);
  await post(request, event.a, '/api/tickets', [
    { prefix: 'A', t_id: 1, first_name: 'Ann', last_name: 'Lee', phone_number: '555-0001', pref: 'CALL' },
    { prefix: 'A', t_id: 2, first_name: 'Bob', last_name: 'Ray', phone_number: '555-0002', pref: 'CALL' }
  ]);
  await post(request, event.a, '/api/baskets', [{ prefix: 'A', b_id: 1, description: 'Wine', donors: 'Smiths', winning_ticket: 0 }]);
}

// B changes ticket 1's phone from what everyone loaded to 555-0888.
async function correctOnB(request, event) {
  await post(request, event.b, '/api/tickets', [{
    prefix: 'A', t_id: 1, first_name: 'Ann', last_name: 'Lee', phone_number: '555-0888', pref: 'CALL',
    base: { first_name: 'Ann', last_name: 'Lee', phone_number: '555-0001', pref: 'CALL' }
  }]);
}

async function serverPhone(request, event) {
  const response = await request.get(url(event.b, '/api/tickets/A/1'));
  return (await response.json()).phone_number;
}

async function openTickets(page, client) {
  await page.goto(url(client, '/web/tickets/A/'));
  await page.locator('#id_from').fill('1');
  await page.locator('#id_to').fill('2');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Ticket 1 phone number', exact: true })).toHaveValue('555-0001');
}

test('a page saving over a newer value shows the newer value and says so', async ({ page, request, event }) => {
  await seed(request, event);
  await openTickets(page, event.a);
  await correctOnB(request, event);

  const dialogs = [];
  page.on('dialog', async (dialog) => {
    dialogs.push(dialog.message());
    await dialog.accept();
  });
  await page.getByRole('textbox', { name: 'Ticket 1 phone number', exact: true }).fill('555-0777');
  await page.getByRole('textbox', { name: 'Ticket 2 last name', exact: true }).fill('Raye');
  await page.getByRole('button', { name: 'Save Marked', exact: true }).click();

  await expect.poll(() => dialogs.length).toBe(1);
  expect(dialogs[0]).toContain('another computer changed');
  expect(dialogs[0]).toContain('Ticket 1 phone number: now "555-0888", yours was "555-0777"');
  await expect(page.getByRole('textbox', { name: 'Ticket 1 phone number', exact: true })).toHaveValue('555-0888');
  expect(await serverPhone(request, event)).toBe('555-0888');
  // The other change went through.
  const two = await (await request.get(url(event.b, '/api/tickets/A/2'))).json();
  expect(two.last_name).toBe('Raye');
  // Typing it again, from the value now shown, replaces it deliberately.
  await page.getByRole('textbox', { name: 'Ticket 1 phone number', exact: true }).fill('555-0777');
  await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
  await expect.poll(() => serverPhone(request, event)).toBe('555-0777');
  expect(dialogs).toHaveLength(1);
});

test('an offline save that arrives after a newer one waits in Settings for Retry', async ({ page, request, event }) => {
  await seed(request, event);
  await openTickets(page, event.a);
  event.a.link.setOnline(false);
  await page.getByRole('textbox', { name: 'Ticket 1 phone number', exact: true }).fill('555-0777');
  await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
  await expect(page.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(0);

  await correctOnB(request, event);
  event.a.link.setOnline(true);
  await expect.poll(async () => {
    const state = await (await request.get(url(event.a, '/api/status'))).json();
    return [state.state, state.pending, state.failed];
  }, { timeout: 20_000 }).toEqual(['connected', 0, 1]);
  expect(await serverPhone(request, event)).toBe('555-0888');

  await page.goto(url(event.a, '/web/settings/'));
  const reason = page.locator('#server_section li').first();
  await expect(reason).toContainText('another computer changed it to "555-0888"');
  await expect(reason).toContainText('"555-0777" was not saved');
  await page.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect.poll(() => serverPhone(request, event), { timeout: 20_000 }).toBe('555-0777');
});

test('a winner from a page loaded before another winner was entered does not replace it', async ({ page, request, event }) => {
  await seed(request, event);
  await page.goto(url(event.a, '/web/drawing/A/'));
  await page.locator('#id_from').fill('1');
  await page.locator('#id_to').fill('1');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  await post(request, event.b, '/api/drawing', [{ prefix: 'A', b_id: 1, winning_ticket: 2, base: { winning_ticket: 0 } }]);

  const dialogs = [];
  page.on('dialog', async (dialog) => {
    dialogs.push(dialog.message());
    await dialog.accept();
  });
  await page.getByRole('spinbutton', { name: 'Basket 1 winning ticket', exact: true }).fill('1');
  await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
  await expect.poll(() => dialogs.length).toBe(1);
  expect(dialogs[0]).toContain('Basket 1 winning ticket: now "2", yours was "1"');
  await expect(page.getByRole('spinbutton', { name: 'Basket 1 winning ticket', exact: true })).toHaveValue('2');
  const basket = await (await request.get(url(event.b, '/api/baskets/A/1'))).json();
  expect(basket.winning_ticket).toBe(2);
});

test('reports read from this computer\'s copy while the server is away say so', async ({ page, request, event }) => {
  await seed(request, event);
  await post(request, event.a, '/api/drawing', [{ prefix: 'A', b_id: 1, winning_ticket: 1, base: { winning_ticket: 0 } }]);
  await page.goto(url(event.a, '/web/reports/byname/A/'));
  await expect(page.getByText('Lee, Ann')).toBeVisible();
  await expect(page.getByText("From this computer's copy")).toHaveCount(0);

  event.a.link.setOnline(false);
  await expect.poll(async () => (await (await request.get(url(event.a, '/api/status'))).json()).state, { timeout: 20_000 })
    .not.toBe('connected');
  await page.reload();
  await expect(page.getByText('Lee, Ann')).toBeVisible();
  await expect(page.getByText("From this computer's copy")).toBeVisible();
  await page.goto(url(event.a, '/web/reports/counts/'));
  await expect(page.getByText("From this computer's copy")).toBeVisible();
});
