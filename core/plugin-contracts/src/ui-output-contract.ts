import type { UIFacade, UIOutput } from './ui.js';

/** Canonical input/output cases used to prove every UI adapter implements the shared model. */
export const uiOutputContractCases: Record<UIOutput['kind'], {
  invoke(ui: UIFacade): void;
  output: UIOutput;
  visibleText: string[];
}> = {
  message: {
    invoke: ui => ui.info('message body', {
      title: 'Message title',
      summary: { Total: 2 },
      sections: [{ header: 'Message section', items: ['section item'] }],
      cause: 'message cause',
      hint: 'message hint',
      command: 'kb next',
      timing: 12,
    }),
    output: {
      kind: 'message',
      level: 'info',
      message: 'message body',
      options: {
        title: 'Message title',
        summary: { Total: 2 },
        sections: [{ header: 'Message section', items: ['section item'] }],
        cause: 'message cause',
        hint: 'message hint',
        command: 'kb next',
        timing: 12,
      },
    },
    visibleText: ['Message title', 'message body', 'Total', '2', 'Message section', 'section item', 'message cause', 'message hint', 'kb next', '12'],
  },
  sideBox: {
    invoke: ui => ui.sideBox({
      title: 'Box title', status: 'success', summary: { Mode: 'fast' },
      sections: [{ header: 'Box section', items: ['box item'] }], timing: 13,
    }),
    output: {
      kind: 'sideBox',
      options: {
        title: 'Box title', status: 'success', summary: { Mode: 'fast' },
        sections: [{ header: 'Box section', items: ['box item'] }], timing: 13,
      },
    },
    visibleText: ['Box title', 'Mode', 'fast', 'Box section', 'box item', '13'],
  },
  chain: {
    invoke: ui => ui.chain([{
      title: 'Chain title', status: 'warning', summary: { Count: 1 },
      sections: [{ header: 'Chain section', items: ['chain item'] }], timing: 14,
    }]),
    output: {
      kind: 'chain',
      items: [{
        title: 'Chain title', status: 'warning', summary: { Count: 1 },
        sections: [{ header: 'Chain section', items: ['chain item'] }], timing: 14,
      }],
    },
    visibleText: ['Chain title', 'Count', '1', 'Chain section', 'chain item', '14'],
  },
  table: {
    invoke: ui => ui.table([{ name: 'Ada', role: 'owner' }], [{ header: 'Name', key: 'name' }, { header: 'Role', key: 'role' }]),
    output: {
      kind: 'table', data: [{ name: 'Ada', role: 'owner' }],
      columns: [{ header: 'Name', key: 'name' }, { header: 'Role', key: 'role' }],
    },
    visibleText: ['Name', 'Role', 'Ada', 'owner'],
  },
  json: {
    invoke: ui => ui.json({ result: true }, { pretty: true }),
    output: { kind: 'json', data: { result: true }, pretty: true },
    visibleText: ['result', 'true'],
  },
};

/** CLI-specific defaults shared with the adapter regression test. */
export const uiMessageLevelDefaultTitleCases: Record<Extract<UIOutput, { kind: 'message' }>['level'], {
  invoke(ui: UIFacade): void;
  title: string;
}> = {
  info: { invoke: ui => ui.info('default title probe'), title: 'Info' },
  success: { invoke: ui => ui.success('default title probe'), title: 'Success' },
  warn: { invoke: ui => ui.warn('default title probe'), title: 'Warning' },
  error: { invoke: ui => ui.error('default title probe'), title: 'Error' },
};
