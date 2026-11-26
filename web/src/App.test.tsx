import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { App } from './App';

describe('App', () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it('renders LoginView when unauthenticated', () => {
    render(<App />);
    expect(screen.getByText('AgentPilot')).toBeInTheDocument();
    expect(screen.getByLabelText(/Bearer token/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Se connecter/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Tester la connexion/i })).toBeInTheDocument();
  });
});
