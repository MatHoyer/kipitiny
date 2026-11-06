import { Stepper } from 'homelab-ui';

const steps = [{ title: 'Connect' }, { title: 'Configure' }, { title: 'Deploy' }];
const run = [
  { title: 'Pull image', hint: 'ghcr.io/acme/api:1.4.2' },
  { title: 'Run migrations', hint: 'Applying 2 of 3' },
  { title: 'Switch traffic' },
];
export const Horizontal = () => <Stepper className="w-96" steps={steps} current={1} />;
export const AllDone = () => <Stepper className="w-96" steps={steps} current={3} />;
export const VerticalBusy = () => <Stepper className="w-80" orientation="vertical" steps={run} current={1} busy />;
export const VerticalFailed = () => <Stepper className="w-80" orientation="vertical" steps={run} current={1} failed />;
