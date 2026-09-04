import type { ReactNode } from 'react';
import {
  CheckCircle2,
  CircleHelp,
  CirclePause,
  Info,
  ShieldAlert,
  TriangleAlert,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';

export type Tone =
  | 'neutral'
  | 'info'
  | 'success'
  | 'warning'
  | 'danger'
  | 'uncertain';
const icons = {
  neutral: CirclePause,
  info: Info,
  success: CheckCircle2,
  warning: TriangleAlert,
  danger: ShieldAlert,
  uncertain: CircleHelp,
};

export function StatusBadge({
  tone = 'neutral',
  children,
}: {
  tone?: Tone;
  children: ReactNode;
}) {
  const Icon = icons[tone];
  return (
    <Badge variant="outline" className="ovs-status" data-tone={tone}>
      <Icon aria-hidden="true" className="size-3.5" />
      {children}
    </Badge>
  );
}

export function ScopeBadge({ scope }: { scope: string }) {
  return (
    <StatusBadge
      tone={
        scope === 'Manage'
          ? 'info'
          : scope === 'Basic Manage'
            ? 'warning'
            : 'neutral'
      }
    >
      {scope}
    </StatusBadge>
  );
}

export function StateDot({ state }: { state: string }) {
  return (
    <span
      aria-hidden="true"
      className="ovs-state-dot"
      data-tone={
        state === 'Up' ? 'success' : state === 'Down' ? 'danger' : 'uncertain'
      }
    />
  );
}

export function Notice({
  tone = 'info',
  title,
  children,
  actions,
  urgent = false,
}: {
  tone?: Tone;
  title: string;
  children?: ReactNode;
  actions?: ReactNode;
  urgent?: boolean;
}) {
  const Icon = icons[tone];
  return (
    <Alert
      role={urgent ? 'alert' : 'status'}
      className="ovs-notice"
      data-tone={tone}
    >
      <Icon aria-hidden="true" />
      <AlertTitle className="font-semibold">{title}</AlertTitle>
      <AlertDescription className="text-current">
        <div>{children}</div>
        {actions && <div className="mt-3 flex flex-wrap gap-2">{actions}</div>}
      </AlertDescription>
    </Alert>
  );
}

export function PageHeader({
  id,
  eyebrow,
  title,
  description,
  scope,
  actions,
}: {
  id: string;
  eyebrow: string;
  title: string;
  description: string;
  scope?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="ovs-page-header">
      <div>
        <p className="ovs-eyebrow">
          {eyebrow}
          <span className="sr-only"> · {id}</span>
        </p>
        <div className="mt-1 flex flex-wrap items-center gap-3">
          <h1>{title}</h1>
          {scope && <ScopeBadge scope={scope} />}
        </div>
        <p className="mt-2 max-w-3xl text-sm text-muted-foreground">
          {description}
        </p>
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}
