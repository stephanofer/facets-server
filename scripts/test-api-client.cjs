// Optional offline script tests. Uses the YAML parser shipped with Bruno CLI;
// the mandatory Go contract checks do not require Node or Bruno.
const assert = require('node:assert/strict');
const { execSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');

const globalModules = execSync('npm root -g', { encoding: 'utf8' }).trim();
const yaml = require(require.resolve('yaml', {
  paths: [path.join(globalModules, '@usebruno', 'cli')]
}));
const root = path.resolve(__dirname, '../api-client');
const documents = new Map();
function load(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    if (entry.isDirectory() && !['node_modules', '.git'].includes(entry.name)) {
      load(path.join(directory, entry.name));
    } else if (entry.isFile() && entry.name.endsWith('.yml')) {
      const file = path.join(directory, entry.name);
      documents.set(path.relative(root, file).replaceAll('\\', '/'), yaml.parse(fs.readFileSync(file, 'utf8')));
    }
  }
}
load(root);
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
function scripts(document, type) {
  return [...(document?.request?.scripts || []), ...(document?.runtime?.scripts || [])]
    .filter(script => script.type === type);
}

function session(initial = {}) {
  const variables = new Map(Object.entries({
    accessToken: 'c_synthetic', spaceId: 'space-1', financeSpaceId: 'space-1',
    selectedAccountId: '', selectedCatalogEntryId: '', selectedOperationId: '',
    selectedDueId: '', selectedSeriesId: '', selectedPlanningResourceId: '',
    ...initial
  }));
  let nextKey = 0;
  const calls = [];
  const responses = new Map();
  const bru = {
    getVar: key => variables.get(key),
    setVar(key, value) {
      assert.notEqual(value, undefined, `Script captured undefined into ${key}`);
      variables.set(key, value);
    },
    interpolate(value) {
      return value.replace(/\{\{\$randomUUID\}\}/g, () => `synthetic-key-${++nextKey}`)
        .replace(/\{\{([^{}]+)\}\}/g, (_, key) => String(variables.get(key) ?? ''));
    },
    async runRequest(target) {
      calls.push(target);
      assert.ok(documents.has(`${target}.yml`), `Missing nested request ${target}`);
      assert.ok(responses.has(target), `Provide a synthetic response for ${target}`);
      return run(target, responses.get(target));
    }
  };
  async function run(target, response = { status: 200, body: {} }) {
    const file = `${target}.yml`;
    const document = documents.get(file);
    assert.equal(document?.info?.type, 'http', `Missing HTTP request ${file}`);
    const parents = [documents.get('opencollection.yml')];
    const segments = target.split('/');
    for (let i = 1; i < segments.length; i++) {
      parents.push(documents.get(`${segments.slice(0, i).join('/')}/folder.yml`));
    }
    let body = document.http.body?.data;
    const req = {
      getName: () => document.info.name,
      getUrl: () => document.http.url,
      getBody: () => {
        if (typeof body !== 'string') return body;
        // Mirror interpolation without converting monetary strings into numbers.
        return JSON.parse(body.replace(/\{\{([^{}]+)\}\}/g, (_, key) => String(variables.get(key) ?? 1)));
      },
      setBody: value => { body = value; }
    };
    const res = { ...response, getHeader: () => undefined };
    for (const owner of [...parents, document]) {
      for (const script of scripts(owner, 'before-request')) {
        await new AsyncFunction('bru', 'req', 'res', script.code)(bru, req, res);
      }
    }
    const submittedBody = req.getBody();
    for (const owner of [document, ...parents.reverse()]) {
      for (const script of scripts(owner, 'after-response')) {
        await new AsyncFunction('bru', 'req', 'res', script.code)(bru, req, res);
      }
    }
    return { ...response, submittedBody };
  }
  return { variables, calls, responses, run };
}

test('all scripts compile and nested request paths resolve', () => {
  for (const [file, document] of documents) {
    for (const type of ['before-request', 'after-response', 'tests']) {
      for (const script of scripts(document, type)) {
        assert.doesNotThrow(() => new AsyncFunction('bru', 'req', 'res', script.code), file);
        for (const [, target] of script.code.matchAll(/bru\.runRequest\("([^"]+)"\)/g)) {
          assert.equal(documents.get(`${target}.yml`)?.info?.type, 'http', `${file}: ${target}`);
        }
      }
    }
  }
});

test('finance folders have unique local order and readable JSON bodies', () => {
  const sequences = new Map();
  for (const [file, document] of documents) {
    if (!file.startsWith('finance/') || file === 'finance/folder.yml') continue;
    const parent = file.endsWith('/folder.yml') ? path.posix.dirname(path.posix.dirname(file)) : path.posix.dirname(file);
    const key = `${parent}/${document.info.seq}`;
    assert.ok(!sequences.has(key), `Duplicate order: ${file} and ${sequences.get(key)}`);
    sequences.set(key, file);
    if (document.http?.body?.type === 'json') {
      const data = document.http.body.data;
      assert.ok(data.includes('\n'), `${file}: body must be multiline`);
      const rendered = data.replace(/\{\{[^{}]+\}\}/g, '1');
      assert.equal(rendered, JSON.stringify(JSON.parse(rendered), null, 2), `${file}: expected two-space JSON indentation`);
    }
  }
});

test('expense creation and replay preserve selection and action key', async () => {
  const s = session({ accountId: 'account-1', selectedAccountId: 'account-1' });
  await s.run('finance/operations/create-financial-operation', { status: 200, body: { id: 'operation-1' } });
  const key = s.variables.get('operationActionKey');
  await s.run('finance/operations/get-financial-operation', { status: 200, body: { revision: 3 } });
  assert.equal(s.variables.get('operationVersion'), 3);
  await s.run('finance/operations/create-financial-operation', { status: 200, body: { id: 'operation-1' } });
  assert.equal(s.variables.get('operationActionKey'), key);
  assert.equal(s.variables.get('operationVersion'), 3);
});

test('account/catalog edits resolve moved detail requests', async () => {
  const s = session({ accountId: 'account-1', catalogEntryId: 'tag-1' });
  s.responses.set('finance/accounts/get-financial-account', { status: 200, body: { version: 7 } });
  s.responses.set('finance/catalog/get-catalog-entry', { status: 200, body: { version: 4 } });
  const account = await s.run('finance/accounts/edit-financial-account');
  const catalog = await s.run('finance/catalog/edit-catalog-entry');
  assert.equal(account.submittedBody.version, 7);
  assert.equal(account.submittedBody.state, 'active');
  assert.equal(catalog.submittedBody.version, 4);
  assert.equal(catalog.submittedBody.archived, false);
  assert.deepEqual(s.calls, ['finance/accounts/get-financial-account', 'finance/catalog/get-catalog-entry']);
});

test('switching due clears prior versions, reminder state and applications', async () => {
  const s = session({ dueId: 'due-2', selectedDueId: 'due-1', dueVersion: 9,
    reminderVersion: 4, reminderConfigureKey: 'old', paymentApplicationId: 'old',
    planningResourceId: 'old-resource', planningAccessVersion: 8 });
  await assert.rejects(s.run('finance/reminders/configure-due-reminder'), /Review get-due-item/);
  for (const key of ['dueVersion', 'reminderVersion', 'reminderConfigureKey', 'paymentApplicationId', 'planningResourceId', 'planningAccessVersion']) {
    assert.equal(s.variables.get(key), '', key);
  }
  await s.run('finance/dues/get-due-item', { status: 200, body: { version: 2, resource_id: 'resource-2' } });
  assert.equal(s.variables.get('dueVersion'), 2);
  assert.equal(s.variables.get('planningResourceId'), 'resource-2');
});

test('space switch cannot reuse financial IDs or retained keys', async () => {
  const s = session({ spaceId: 'space-2', accountId: 'account-1', dueId: 'due-1',
    operationId: 'operation-1', operationActionKey: 'old', dueCreateKey: 'old', seriesCreateKey: 'old' });
  await s.run('finance/accounts/list-financial-accounts', { status: 200, body: { items: [] } });
  for (const key of ['accountId', 'dueId', 'operationId', 'operationActionKey', 'dueCreateKey', 'seriesCreateKey']) {
    assert.equal(s.variables.get(key), '', key);
  }
});

test('capacity and application use the same effect including ordinal zero', async () => {
  const s = session({ operationId: 'operation-1', selectedOperationId: 'operation-1',
    dueId: 'due-1', selectedDueId: 'due-1', dueVersion: 2, effectOrdinal: 2 });
  await s.run('finance/payments/get-movement-application-capacity', { status: 200, body: { revision: 5 } });
  const applied = await s.run('finance/payments/apply-payment');
  assert.equal(applied.submittedBody.effect, 2);
  assert.equal(applied.submittedBody.revision, 5);
  s.variables.set('effectOrdinal', 0);
  const zero = await s.run('finance/payments/apply-payment');
  assert.equal(zero.submittedBody.effect, 0);
});

test('payment creation does not guess the operation revision', async () => {
  const s = session({ accountId: 'account-1', selectedAccountId: 'account-1',
    dueId: 'due-1', selectedDueId: 'due-1', dueVersion: 2,
    operationId: 'old', selectedOperationId: 'old', operationVersion: 9, operationCorrectionKey: 'old' });
  await s.run('finance/payments/create-payment-operation', { status: 200, body: { id: 'new' } });
  assert.equal(s.variables.get('operationId'), 'new');
  assert.equal(s.variables.get('operationVersion'), '');
  assert.equal(s.variables.get('operationCorrectionKey'), '');
});

test('application list preserves deliberate selection and clears empty results', async () => {
  const s = session({ dueId: 'due-1', selectedDueId: 'due-1', paymentApplicationId: 'second' });
  await s.run('finance/payments/list-due-applications', { status: 200, body: [{ id: 'first' }, { id: 'second' }] });
  assert.equal(s.variables.get('paymentApplicationId'), 'second');
  await s.run('finance/payments/list-due-applications', { status: 200, body: [] });
  assert.equal(s.variables.get('paymentApplicationId'), '');
});

test('reminder zero version is valid and submitted versions survive retry', async () => {
  const s = session({ dueId: 'due-1', selectedDueId: 'due-1', dueVersion: 2 });
  await s.run('finance/reminders/get-due-reminder', { status: 200, body: { version: 0 } });
  const first = await s.run('finance/reminders/configure-due-reminder');
  const key = s.variables.get('reminderConfigureKey');
  const retry = await s.run('finance/reminders/configure-due-reminder');
  assert.equal(first.submittedBody.version, 0);
  assert.deepEqual(retry.submittedBody, first.submittedBody);
  assert.equal(s.variables.get('reminderConfigureKey'), key);
  s.variables.set('reminderVersion', 1);
  await assert.rejects(s.run('finance/reminders/snooze-due-reminder'), /choose reminderSnoozeUntil/);
});

test('series captures real generation boundary and clears prior selection state', async () => {
  const s = session({ seriesId: 'series-2', selectedSeriesId: 'series-1', seriesVersion: 7,
    seriesGeneratedUntil: 'old', seriesEditKey: 'old', seriesReminderVersion: 2 });
  await s.run('finance/series/get-recurring-series', { status: 200, body: { version: 3, generated_until: '2027-02-01' } });
  assert.equal(s.variables.get('seriesVersion'), 3);
  assert.equal(s.variables.get('seriesGeneratedUntil'), '2027-02-01');
  assert.equal(s.variables.get('seriesEditKey'), '');
  assert.equal(s.variables.get('seriesReminderVersion'), '');
});

test('planning access retains the submitted version for retry', async () => {
  const s = session({ planningResourceId: 'plan-1' });
  await s.run('finance/planning-access/get-planning-access', { status: 200, body: { version: 3, responsible_member_id: 'member-1' } });
  const first = await s.run('finance/planning-access/edit-planning-access');
  const key = s.variables.get('planningAccessKey');
  const retry = await s.run('finance/planning-access/edit-planning-access');
  assert.deepEqual(first.submittedBody, retry.submittedBody);
  assert.equal(s.variables.get('planningAccessKey'), key);
  assert.equal(s.variables.get('planningAccessVersion'), 3);
});

test('reconciliation uses preview balance, rejects unknown adjustment and preserves zero', async () => {
  const s = session({ accountId: 'account-1', selectedAccountId: 'account-1' });
  await s.run('finance/reconciliation/preview-account-reconciliation', { status: 200, body: { calculated_minor: '10800' } });
  const command = await s.run('finance/reconciliation/reconcile-account', { status: 200, body: { id: 'comparison-1' } });
  assert.equal(command.submittedBody.expected_calculated_minor, '10800');
  const key = s.variables.get('reconciliationActionKey');
  const retry = await s.run('finance/reconciliation/reconcile-account', { status: 200, body: { id: 'comparison-1' } });
  assert.deepEqual(retry.submittedBody, command.submittedBody);
  assert.equal(s.variables.get('reconciliationActionKey'), key);
  await s.run('finance/reconciliation/preview-account-reconciliation', { status: 200, body: { calculated_minor: null } });
  await assert.rejects(s.run('finance/reconciliation/reconcile-account'), /known balance/);
  await s.run('finance/reconciliation/preview-account-reconciliation', { status: 200, body: { calculated_minor: '0' } });
  assert.equal(s.variables.get('reconciliationCalculatedMinor'), '0');
});

test('error responses never replace selected IDs or versions', async () => {
  const s = session({ accountId: 'account-1', selectedAccountId: 'account-1',
    dueId: 'due-1', selectedDueId: 'due-1', dueVersion: 3 });
  await s.run('finance/dues/get-due-item', { status: 403, body: { code: 'forbidden' } });
  await s.run('finance/dues/create-due-item', { status: 409, body: { code: 'resource_conflict' } });
  assert.equal(s.variables.get('dueId'), 'due-1');
  assert.equal(s.variables.get('dueVersion'), 3);
  assert.ok(s.variables.get('dueCreateKey'));
});

test('every retained-key command can replay its submitted body without an intervening read', async t => {
  const cases = [
    ['operations/create-financial-operation', 'operationActionKey', 'operation-1'],
    ['operations/correct-financial-operation', 'operationCorrectionKey', 'operation-1'],
    ['operations/void-financial-operation', 'operationVoidKey', 'operation-1'],
    ['reconciliation/reconcile-account', 'reconciliationActionKey', 'comparison-1'],
    ['reconciliation/check-account-consistency', 'consistencyActionKey', 'check-1'],
    ['dues/create-due-item', 'dueCreateKey', 'due-1'],
    ['dues/edit-due-item', 'dueEditKey', 'due-1'],
    ['payments/apply-payment', 'paymentApplyKey', 'operation-1'],
    ['payments/unlink-payment', 'paymentUnlinkKey', 'application-1'],
    ['payments/create-payment-operation', 'paymentOperationKey', 'operation-1'],
    ['series/create-recurring-series', 'seriesCreateKey', 'series-1'],
    ['series/edit-recurring-series', 'seriesEditKey', 'series-1'],
    ['series/generate-recurring-occurrences', 'seriesGenerateKey', 'series-1'],
    ['planning-access/edit-planning-access', 'planningAccessKey', 'plan-1'],
    ['planning-access/recover-planning-resource', 'planningRecoveryKey', 'plan-1'],
    ['reminders/configure-due-reminder', 'reminderConfigureKey', 'due-1'],
    ['reminders/snooze-due-reminder', 'reminderSnoozeKey', 'due-1'],
    ['reminders/configure-series-reminder', 'seriesReminderKey', 'series-1']
  ];
  for (const [target, key, id] of cases) {
    await t.test(target, async () => {
      const s = session({
        accountId: 'account-1', selectedAccountId: 'account-1', accountVersion: 2,
        operationId: 'operation-1', selectedOperationId: 'operation-1', operationVersion: 3,
        dueId: 'due-1', selectedDueId: 'due-1', dueVersion: 4,
        seriesId: 'series-1', selectedSeriesId: 'series-1', seriesVersion: 5,
        seriesGeneratedUntil: '2027-02-01', effectOrdinal: 0, paymentApplicationId: 'application-1',
        planningResourceId: 'plan-1', selectedPlanningResourceId: 'plan-1',
        planningAccessVersion: 6, planningResponsibleMemberId: 'member-1',
        recoveryPlanningId: 'plan-1', recoveryPlanningVersion: 7,
        reminderVersion: 1, reminderSnoozeUntil: '2027-01-01T09:00:00Z',
        seriesReminderVersion: 0, reconciliationCalculatedMinor: '10800'
      });
      const response = { status: 200, body: { id } };
      const first = await s.run(`finance/${target}`, response);
      const actionKey = s.variables.get(key);
      assert.ok(actionKey);
      const retry = await s.run(`finance/${target}`, response);
      assert.equal(s.variables.get(key), actionKey);
      assert.deepEqual(retry.submittedBody, first.submittedBody);
    });
  }
});
