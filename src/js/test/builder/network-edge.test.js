// @vitest-environment happy-dom
// The issue mark of a connection: a cross or a triangle in its label, or
// alone at the middle of a line with no label, and the hidden text that
// describes the connection.

import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';

import NetworkEdge from '@/components/builder/edges/NetworkEdge.vue';
import { edgeIssueId } from '@/builder/adapters/vueflow.js';

enableAutoUnmount(afterEach);

let labels = null;

beforeEach(() => {
  labels = document.createElement('div');
  document.body.append(labels);
});

afterEach(() => {
  labels.remove();
});

function mountEdge({ label = 'EXP', issue = null } = {}) {
  return mount(NetworkEdge, {
    attachTo: document.body,
    props: {
      id: 'e1',
      sourceX: 0,
      sourceY: 0,
      targetX: 200,
      targetY: 100,
      label,
      data: { style: { token: 0, pattern: 'solid', label: 'EXP' }, issue },
    },
    global: { provide: { builderEdgeLabels: labels } },
  });
}

const marks = () => [...labels.querySelectorAll('[data-testid="edge-issue"]')];
const hiddenText = () => document.getElementById(edgeIssueId('e1'));

describe('a connection the diagram checks flag', () => {
  test('has a triangle in its label for warnings, and hidden text that lists them', () => {
    mountEdge({
      issue: { level: 'warning', text: '1 warning: unknown network.' },
    });

    const label = labels.querySelector('.builder-edge__label');
    const [mark] = marks();

    expect(marks()).toHaveLength(1);
    expect(label.contains(mark)).toBe(true);
    expect(label.getAttribute('aria-hidden')).toBe('true');
    expect(label.textContent.trim()).toBe('EXP');
    expect(mark.dataset.level).toBe('warning');
    expect(mark.querySelector('.builder-icon--warning')).not.toBeNull();
    expect(hiddenText().hidden).toBe(true);
    expect(hiddenText().textContent).toBe('1 warning: unknown network.');
  });

  test('has a cross alone at the middle of a line with no label, for errors', () => {
    mountEdge({
      label: '',
      issue: { level: 'error', text: '1 error: no network.' },
    });

    const [mark] = marks();

    expect(labels.querySelector('.builder-edge__label')).toBeNull();
    expect(mark.classList.contains('builder-edge__issue--alone')).toBe(true);
    expect(mark.getAttribute('aria-hidden')).toBe('true');
    expect(mark.dataset.level).toBe('error');
    expect(mark.dataset.edgeId).toBe('e1');
    expect(mark.querySelector('.builder-icon--close')).not.toBeNull();
    expect(hiddenText().textContent).toBe('1 error: no network.');
  });

  test('loses its mark and its text once the issues are gone', async () => {
    const wrapper = mountEdge({
      issue: { level: 'warning', text: '1 warning: unknown network.' },
    });

    await wrapper.setProps({
      data: { style: { token: 0, pattern: 'solid', label: 'EXP' } },
    });

    expect(marks()).toHaveLength(0);
    expect(hiddenText()).toBeNull();
    expect(
      labels.querySelector('.builder-edge__label').textContent.trim(),
    ).toBe('EXP');
  });

  test('a line with no label and no issues draws nothing in the label layer', () => {
    mountEdge({ label: '' });

    expect(labels.children).toHaveLength(0);
  });
});
