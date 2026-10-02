import { test, expect } from './client-fixture.js';

// These tests catch removal of any form's leave-save registration, a save
// that omits a marked row, or a request that dies with the departing page.
// Read the persisted API data independently; no request routing or mocks:
// Playwright route interception can itself hold up unload keepalive requests.
const forms = [
  { name: 'Tickets', path: '/web/tickets/A/', labels: ['Ticket 1 first name', 'Ticket 2 first name'],
    values: ['Alice', 'Hazel'], endpoint: '/api/tickets/A/1/2', field: 'first_name' },
  { name: 'Baskets', path: '/web/baskets/A/', labels: ['Basket 1 description', 'Basket 2 description'],
    values: ['Updated basket one', 'Updated basket two'], endpoint: '/api/baskets/A/1/2', field: 'description' },
  { name: 'Drawing', path: '/web/drawing/A/', labels: ['Basket 1 winning ticket', 'Basket 2 winning ticket'],
    values: [11, 22], endpoint: '/api/baskets/A/1/2', field: 'winning_ticket' },
  { name: 'Search', path: '/web/search/tickets/', labels: ['Ticket A 1 first name', 'Ticket A 2 first name'],
    values: ['Alice', 'Hazel'], endpoint: '/api/tickets/A/1/2', field: 'first_name' }
];

test.beforeEach(async ({ request }) => {
  for (const [path, data] of [
    ['/api/prefixes', [{ prefix: 'A', color: 'green', weight: 1 }]],
    ['/api/tickets', [1, 2, 11, 22].map((t_id) => ({
      prefix: 'A', t_id, first_name: `Seed${t_id}`,
      last_name: t_id < 3 ? 'Browserprobe' : 'Winner', phone_number: '555-0100', pref: 'CALL'
    }))],
    ['/api/baskets', [1, 2].map((b_id) => ({
      prefix: 'A', b_id, description: `Seed basket ${b_id}`, donors: 'Test donor', winning_ticket: 0
    }))]
  ]) {
    const response = await request.post(path, { data });
    expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
  }
});

test('A description saved from the Baskets form leaves a drawn winner alone', async ({ page, request }) => {
  const drawing = await request.post('/api/drawing', {
    data: [{ prefix: 'A', b_id: 1, winning_ticket: 11 }]
  });
  expect(drawing.ok()).toBeTruthy();
  await page.goto('/web/baskets/A/');
  await page.locator('#id_from').fill('1');
  await page.locator('#id_to').fill('2');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  await page.getByRole('textbox', { name: 'Basket 1 description', exact: true }).fill('Only metadata edited');
  await page.getByRole('link', { name: 'Main Menu', exact: true }).click();
  await expect(page).toHaveURL(/\/web\/$/);
  await expect.poll(async () => {
    const response = await request.get('/api/baskets/A/1');
    return response.json();
  }).toMatchObject({ description: 'Only metadata edited', winning_ticket: 11 });
});

for (const form of forms) {
  for (const leave of ['hidden', 'navigation', 'close']) {
    test(`${form.name}: marked rows survive ${leave}`, async ({ page, request }) => {
      const dialogs = [];
      page.on('dialog', async (dialog) => {
        dialogs.push(`${dialog.type()}: ${dialog.message()}`);
        await dialog.accept();
      });
      await page.goto(form.path);
      if (form.name === 'Search') {
        await page.locator('#search_last_name').fill('Browserprobe');
        await page.locator('#search_last_name').press('Enter');
      } else {
        await page.locator('#id_from').fill('1');
        await page.locator('#id_to').fill('2');
        await page.getByRole('button', { name: 'Go', exact: true }).click();
      }
      for (let i = 0; i < form.labels.length; i++) {
        await page.getByRole('textbox', { name: form.labels[i], exact: true })
          .or(page.getByRole('spinbutton', { name: form.labels[i], exact: true }))
          .fill(String(form.values[i]));
      }
      await expect(page.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(2);

      if (leave === 'hidden') {
        // Headless Chromium keeps tabs visible. Exercise the browser's
        // visibility event contract explicitly; this is not an OS discard test.
        await page.evaluate(() => {
          Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true });
          Object.defineProperty(document, 'hidden', { value: true, configurable: true });
          document.dispatchEvent(new Event('visibilitychange'));
        });
      } else if (leave === 'navigation') {
        await page.getByRole('link', { name: 'Main Menu', exact: true }).click();
        await expect(page).toHaveURL(/\/web\/$/);
      } else {
        await page.close({ runBeforeUnload: true });
        await expect.poll(() => page.isClosed()).toBe(true);
      }

      await expect.poll(async () => {
        const response = await request.get(form.endpoint);
        expect(response.ok()).toBeTruthy();
        return (await response.json()).map((row) => row[form.field]);
      }, { message: `${form.name} saves both marked rows on ${leave}` }).toEqual(form.values);

      if (leave === 'hidden') {
        // Assert before closing: a working beforeunload hook must not mask
        // a broken visibility hook. Coming back must keep the edit position.
        await expect(page.locator('tbody').getByRole('button', { name: 'No', exact: true })).toHaveCount(2);
        await page.evaluate(() => {
          delete document.visibilityState;
          delete document.hidden;
          document.dispatchEvent(new Event('visibilitychange'));
        });
        await expect(page.getByLabel(form.labels[1], { exact: true })).toBeFocused();
      }
      expect(dialogs).toEqual([]);
    });
  }
}
