import { test, expect } from '@playwright/test';

test.describe('Antares Observatory Showcase & Milestone M3 E2E', () => {
  test('Overview: Loads hero claims and guided tour engine', async ({ page }) => {
    await page.goto('/overview');
    await expect(page).toHaveTitle(/Antares Observatory/);

    // Assert hero claims cards are rendered
    await expect(page.getByRole('heading', { name: 'Antares Observatory' })).toBeVisible();
    await expect(page.getByText('Sequential SET (P=1)')).toBeVisible();
    await expect(page.getByText('Pipelined GET (P=16)')).toBeVisible();
    await expect(page.getByText('AOF SIGKILL Durability')).toBeVisible();

    // Assert status bar shows active Antares address
    await expect(page.getByText('127.0.0.1:6380')).toBeVisible();
  });

  test('Navigation: Navigates through all core routes smoothly', async ({ page }) => {
    await page.goto('/overview');

    // Metrics
    await page.locator('#nav-metrics').click();
    await expect(page.getByRole('heading', { name: /Telemetry & Performance/ })).toBeVisible();

    // Benchmarks
    await page.locator('#nav-benchmarks').click();
    await expect(page.getByRole('heading', { name: /Benchmarks/ })).toBeVisible();

    // Durability Lab
    await page.locator('#nav-durability').click();
    await expect(page.getByRole('heading', { name: /Durability Lab/ })).toBeVisible();

    // Interview Mode
    await page.locator('#nav-interview').click();
    await expect(page.getByRole('heading', { name: 'Interview & Defense Mode' })).toBeVisible();

    // Replication Lab
    await page.locator('#nav-replication').click();
    await expect(page.getByRole('heading', { name: 'Replication Lab' })).toBeVisible();

    // Keyspace
    await page.locator('#nav-keyspace').click();
    await expect(page).toHaveURL(/.*keyspace/);
  });

  test('Replication Lab (U10): Verifies live topology, circular backlog ring, and Story 5', async ({ page }) => {
    await page.goto('/replication');
    await expect(page.getByRole('heading', { name: 'Replication Lab' })).toBeVisible();
    await expect(page.getByText('Live Node Topology')).toBeVisible();
    await expect(page.getByText('1 MB Circular Backlog Ring Buffer')).toBeVisible();
    await expect(page.getByText('Story 5: PSYNC Resynchronization Lab')).toBeVisible();

    // Click "Simulate Partial Resync (+CONTINUE)"
    const continueBtn = page.getByRole('button', { name: 'Simulate Partial Resync (+CONTINUE)' });
    await expect(continueBtn).toBeVisible();
    await continueBtn.click();

    // Assert decision output
    await expect(page.getByText('+CONTINUE', { exact: true })).toBeVisible({ timeout: 10000 });
  });

  test('Interview Mode (U11): Teleprompter, Story 6 visualizer, and QBank', async ({ page }) => {
    await page.goto('/interview');
    await expect(page.getByRole('heading', { name: 'Interview & Defense Mode' })).toBeVisible();

    // Check teleprompter tabs
    await expect(page.getByText('Timed Teleprompter & Pitches')).toBeVisible();
    await page.getByRole('button', { name: '30s' }).click();
    await expect(page.getByText('30-Second Elevator Pitch')).toBeVisible();

    await page.getByRole('button', { name: '10min' }).click();
    await expect(page.getByText('10-Minute Technical Deep-Dive')).toBeVisible();

    // Switch to 6-Story Live Demo tab
    await page.getByRole('button', { name: /6-Story Live Demo Suite/ }).click();
    await expect(page.getByText('Story 6: Transaction State Machine Visualizer')).toBeVisible();

    // Run Story 6 Clean Commit
    const cleanCommitBtn = page.getByRole('button', { name: 'Clean Commit' });
    await expect(cleanCommitBtn).toBeVisible();
    await cleanCommitBtn.click();
    await expect(page.getByText('COMMITTED', { exact: true })).toBeVisible({ timeout: 10000 });
    await expect(page.getByText(/Single AOF Frame: YES/)).toBeVisible();

    // Switch to QBank Defense Matrix
    await page.getByRole('button', { name: /Q-Bank & STAR Defense Matrix/ }).click();
    await expect(page.getByText('What was the hardest bug you diagnosed and fixed in Antares?')).toBeVisible();

    // Switch to Rehearsal Checklist
    await page.getByRole('button', { name: /Rehearsal Checklist/ }).click();
    await expect(page.getByText('Interview Readiness Checklist')).toBeVisible();
  });
});
