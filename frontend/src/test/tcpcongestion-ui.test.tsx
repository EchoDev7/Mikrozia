import { cleanup, fireEvent, screen } from '@testing-library/react';
import { FormProvider, useForm, useFormContext, useWatch } from 'react-hook-form';
import { afterEach, describe, expect, it } from 'vitest';

import { TcpCongestionField } from '@/components/xray/TcpCongestionField';
import { chooseSelectOption, renderWithProviders } from './test-utils';

afterEach(() => {
  cleanup();
});

function FormValueProbe({ fieldPath }: { fieldPath: string }) {
  const { control, getValues } = useFormContext();
  const watched = useWatch({ control, name: fieldPath });
  const current = getValues(fieldPath);
  return (
    <>
      <span data-testid="watched-value">
        {watched === undefined ? 'UNDEFINED' : String(watched)}
      </span>
      <span data-testid="getvalues-value">
        {current === undefined ? 'UNDEFINED' : String(current)}
      </span>
    </>
  );
}

function Wrapper({ defaultValues }: { defaultValues?: Record<string, unknown> }) {
  const methods = useForm({ defaultValues });
  return (
    <FormProvider {...methods}>
      <TcpCongestionField
        id="tcp-congestion-select"
        name={['streamSettings', 'sockopt', 'tcpcongestion']}
      />
      <FormValueProbe fieldPath="streamSettings.sockopt.tcpcongestion" />
    </FormProvider>
  );
}

describe('TcpCongestionField UI behavior and FormField integration', () => {
  it('starts with undefined form value and defaults to OS Default without legacy warning', () => {
    renderWithProviders(<Wrapper defaultValues={{ streamSettings: { sockopt: {} } }} />);
    expect(screen.getByTestId('watched-value').textContent).toBe('UNDEFINED');
    expect(screen.getByTestId('getvalues-value').textContent).toBe('UNDEFINED');
    expect(screen.getByText(/OS Default/i)).toBeTruthy();
    expect(screen.queryByText(/omitted from RouterOS runtime config/i)).toBeNull();
  });

  it('preserves legacy bbr without interaction and lets user select cubic, reno, and OS Default', () => {
    renderWithProviders(
      <Wrapper defaultValues={{ streamSettings: { sockopt: { tcpcongestion: 'bbr' } } }} />,
    );
    expect(screen.getByTestId('watched-value').textContent).toBe('bbr');
    expect(screen.getByTestId('getvalues-value').textContent).toBe('bbr');
    expect(screen.getByText(/Legacy — ignored on RouterOS/i)).toBeTruthy();
    expect(screen.getByText(/omitted from RouterOS runtime config/i)).toBeTruthy();

    chooseSelectOption('tcp-congestion-select', 'cubic');
    expect(screen.getByTestId('watched-value').textContent).toBe('cubic');
    expect(screen.getByTestId('getvalues-value').textContent).toBe('cubic');
    expect(screen.queryByText(/omitted from RouterOS runtime config/i)).toBeNull();

    chooseSelectOption('tcp-congestion-select', 'reno');
    expect(screen.getByTestId('watched-value').textContent).toBe('reno');
    expect(screen.getByTestId('getvalues-value').textContent).toBe('reno');

    chooseSelectOption('tcp-congestion-select', 'OS Default (Unset)');
    expect(screen.getByTestId('watched-value').textContent).toBe('UNDEFINED');
    expect(screen.getByTestId('getvalues-value').textContent).toBe('UNDEFINED');
  });

  it('clears legacy bbr to undefined and removes alert when clear icon is clicked', () => {
    const { container } = renderWithProviders(
      <Wrapper defaultValues={{ streamSettings: { sockopt: { tcpcongestion: 'bbr' } } }} />,
    );
    expect(screen.getByTestId('watched-value').textContent).toBe('bbr');
    expect(screen.getByText(/omitted from RouterOS runtime config/i)).toBeTruthy();

    const clearBtn = container.querySelector('.ant-select-clear');
    expect(clearBtn).toBeTruthy();
    fireEvent.mouseDown(clearBtn as Element);
    fireEvent.click(clearBtn as Element);

    expect(screen.getByTestId('watched-value').textContent).toBe('UNDEFINED');
    expect(screen.getByTestId('getvalues-value').textContent).toBe('UNDEFINED');
    expect(screen.queryByText(/omitted from RouterOS runtime config/i)).toBeNull();
  });
});
