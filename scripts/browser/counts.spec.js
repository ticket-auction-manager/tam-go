import { test, expect } from './client-fixture.js';

test('a prefix named Total is a row of its own beside the total', async ({ page, request }) => {
  for (const [path, data] of [
    ['/api/prefixes', [{ prefix: 'Total', color: 'green', weight: 1 }, { prefix: 'B', color: 'blue', weight: 2 }]],
    ['/api/tickets', [
      { prefix: 'Total', t_id: 1, first_name: 'Jo', last_name: 'Ann', phone_number: '5', pref: 'CALL' },
      { prefix: 'Total', t_id: 2, first_name: 'Joa', last_name: 'nn', phone_number: '5', pref: 'CALL' },
      { prefix: 'B', t_id: 1, first_name: 'Jo', last_name: 'Ann', phone_number: '5', pref: 'CALL' }
    ]]
  ]) {
    const response = await request.post(path, { data });
    expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
  }
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/web/reports/counts/');
  const rows = page.locator('tbody tr');
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toHaveText(/Total\s*2\s*2/);
  await expect(rows.nth(1)).toHaveText(/B\s*1\s*1/);
  await expect(rows.nth(2)).toHaveText(/Total\s*2\s*3/);
  expect(errors).toEqual([]);
});
