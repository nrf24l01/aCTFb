import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

/**
 * Component-layout fixture for the long INSTANCE task form.
 *
 * The default profile disables runtime instances (`deployment.enabled=false`),
 * so the approved-template binding endpoint is not registered and a browser run
 * cannot open a real INSTANCE template that carries many parameters. This
 * fixture mounts the real dialog with a long approved-template parameter list to
 * prove the approved layout contract:
 *
 * - title, content editor and the template binding with its parameters are in
 *   the main column, below the editor;
 * - format, points, category, submission policy and lifetime stay in the
 *   settings rail;
 * - one scrolling dialog body, no duplicated field IDs, every parameter
 *   labelled, secret parameters write-only.
 *
 * It is NOT provider execution and NOT a runtime claim: no provider, driver,
 * instance, credential or network call is involved. The approved-template list
 * is a mocked service response, exactly as the browser cannot reach it under
 * this profile.
 */

const createTask = vi.fn().mockResolvedValue({})
const listApprovedTemplates = vi.fn()

vi.mock('@/features/boards/api/boards', () => ({
  boardsApi: {
    createTask: (...args: unknown[]) => createTask(...args),
    updateTask: vi.fn().mockResolvedValue({}),
  },
}))

vi.mock('@/features/instances/api', () => ({
  instancesApi: {
    listApprovedTemplates: (...args: unknown[]) => listApprovedTemplates(...args),
  },
}))

import TaskEditorDialog from './TaskEditorDialog.vue'

const templateUuid = '11111111-1111-4111-8111-111111111111'
const longLabel = 'Container image registry mirror URL for the isolated build runner (required by policy)'
const longDefault = `https://registry.example.test/${'segment/'.repeat(12)}`

const parameters = [
  { key: 'mirror', label: longLabel, type: 'string', required: true, secret: false, default: longDefault },
  { key: 'replicas', label: 'Replica count', type: 'int', required: true, secret: false, default: '3' },
  { key: 'tls', label: 'TLS enabled', type: 'bool', required: false, secret: false, default: 'true' },
  { key: 'profile', label: 'Sizing profile', type: 'choice', required: true, secret: false, options: ['small', 'medium', 'large'] },
  { key: 'bootstrap', label: 'Bootstrap script', type: 'textarea', required: false, secret: false, default: '#!/bin/sh\necho ready\n' },
  { key: 'flag', label: 'Environment flag', type: 'string', required: true, secret: true },
  ...Array.from({ length: 18 }, (_, index) => ({
    key: `extra${index}`,
    label: `Additional environment value ${index} with a deliberately long descriptive label`,
    type: 'string',
    required: index % 2 === 0,
    secret: false,
    default: `value-${index}`,
  })),
]

function byId(id: string): HTMLElement {
  const element = document.getElementById(id)
  expect(element, `missing #${id}`).not.toBeNull()
  return element!
}

describe('TaskEditorDialog long INSTANCE layout fixture (not provider execution)', () => {
  beforeEach(() => {
    createTask.mockClear()
    listApprovedTemplates.mockReset()
    listApprovedTemplates.mockResolvedValue({
      items: [
        {
          templateUuid,
          version: 2,
          slug: 'long-parameters',
          name: 'Long parameters',
          parametersPolicy: {},
          parameters,
        },
      ],
    })
  })

  it('keeps template parameters in main, lifetime in the rail and one scroller', async () => {
    const wrapper = mount(TaskEditorDialog, {
      props: {
        open: true,
        boardUuid: '22222222-2222-4222-8222-222222222222',
        moduleUuid: '33333333-3333-4333-8333-333333333333',
        allowedTaskTypes: ['INSTANCE'],
      },
      attachTo: document.body,
    })
    await flushPromises()

    const approved = byId('task-instance-approved') as unknown as HTMLSelectElement
    approved.value = `${templateUuid}:2`
    approved.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()

    const main = document.querySelector('[data-slot="authoring-main"]')!
    const settings = document.querySelector('[data-slot="authoring-settings"]')!
    expect(main).not.toBeNull()
    expect(settings).not.toBeNull()

    // Exactly one scrolling body; header and footer are outside it.
    const scrollers = document.querySelectorAll('[data-slot="authoring-scroll"]')
    expect(scrollers).toHaveLength(1)
    expect(document.querySelector('[data-slot="dialog-header"]')!.closest('[data-slot="authoring-scroll"]')).toBeNull()
    expect(document.querySelector('[data-slot="dialog-footer"]')!.closest('[data-slot="authoring-scroll"]')).toBeNull()

    // Title, editor, binding and every parameter are in the main column.
    for (const id of ['task-title', 'task-description', 'task-instance-approved']) {
      expect(byId(id).closest('[data-slot="authoring-main"]'), `#${id} must be in main`).toBe(main)
    }
    const parameterIds = parameters.map((parameter) => (
      parameter.secret ? `secret-${parameter.key}` : `param-${parameter.key}`
    ))
    expect(parameterIds).toHaveLength(24)
    for (const id of parameterIds) {
      expect(byId(id).closest('[data-slot="authoring-main"]'), `#${id} must be in main`).toBe(main)
      expect(byId(id).closest('[data-slot="authoring-settings"]')).toBeNull()
    }

    // The long label and default value are rendered as text, not truncated.
    expect(document.body.textContent).toContain(longLabel)
    expect((byId('param-mirror') as HTMLInputElement).value).toBe(longDefault)

    // Settings rail keeps the existing policy fields, never a parameter input.
    for (const id of ['task-choice', 'task-points', 'task-category']) {
      expect(byId(id).closest('[data-slot="authoring-settings"]'), `#${id} must be in the rail`).toBe(settings)
    }
    const custom = Array.from(
      document.querySelectorAll<HTMLButtonElement>('[data-slot="authoring-settings"] button'),
    ).find((button) => button.textContent?.trim() === 'Custom')
    expect(custom, 'missing the Custom lifetime control').toBeTruthy()
    custom!.click()
    await flushPromises()
    expect(byId('task-lifetime-initial').closest('[data-slot="authoring-settings"]')).toBe(settings)
    expect(byId('task-lifetime-initial').closest('[data-slot="authoring-main"]')).toBeNull()

    // No duplicated field IDs anywhere in the dialog.
    const ids = Array.from(document.querySelectorAll('[id]')).map((element) => element.id)
    expect(new Set(ids).size).toBe(ids.length)

    // Every parameter has a real label; secrets are write-only password inputs.
    for (const id of parameterIds) {
      const label = document.querySelector(`label[for="${id}"]`)
      expect(label, `missing label for #${id}`).not.toBeNull()
    }
    const secret = byId('secret-flag') as HTMLInputElement
    expect(secret.getAttribute('type')).toBe('password')
    expect(secret.value).toBe('')
    expect(secret.getAttribute('autocomplete')).toBe('new-password')

    // Direct authoring: no outer Write/Preview tabs on this surface either.
    expect(document.querySelectorAll('[role="tab"]')).toHaveLength(0)

    wrapper.unmount()
  })
})
