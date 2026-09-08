'use client';

import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../../app/globals.css';
import { Button } from '../../components/ui/button';
import { ButtonGroup } from '../../components/ui/button-group';
import { Input } from '../../components/ui/input';
import { Label } from '../../components/ui/label';
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupTextarea,
  InputGroupButton,
} from '../../components/ui/input-group';
import { Field, FieldLabel } from '../../components/ui/field';
import {
  Breadcrumb,
  BreadcrumbList,
  BreadcrumbItem,
  BreadcrumbPage,
  BreadcrumbLink,
} from '../../components/ui/breadcrumb';
import {
  Pagination,
  PaginationContent,
  PaginationItem,
  PaginationLink,
} from '../../components/ui/pagination';
import { ItemGroup, Item, ItemSeparator } from '../../components/ui/item';
import {
  InputOTP,
  InputOTPGroup,
  InputOTPSlot,
  InputOTPSeparator,
} from '../../components/ui/input-otp';
import { Spinner } from '../../components/ui/spinner';
import {
  Carousel,
  CarouselContent,
  CarouselItem,
  CarouselPrevious,
  CarouselNext,
} from '../../components/ui/carousel';
import {
  ChartContainer,
  ChartTooltipContent,
  ChartLegendContent,
} from '../../components/ui/chart';
import {
  Command,
  CommandInput,
  CommandList,
  CommandItem,
} from '../../components/ui/command';
import {
  Combobox,
  ComboboxInput,
  ComboboxContent,
  ComboboxList,
  ComboboxItem,
} from '../../components/ui/combobox';
import { useIsMobile } from '../../hooks/use-mobile';

function TemplateReview() {
  const mobile = useIsMobile();
  const [result, setResult] = useState('Ready');
  const [count, setCount] = useState(3);
  const [dark, setDark] = useState(false);
  return (
    <main
      className={`mx-auto grid max-w-4xl gap-5 bg-background p-8 text-foreground ${dark ? 'dark' : ''}`}
      // This isolated entry has no next/font layout; keep screenshots independent
      // of remote font delivery by using the application's system fallback.
      style={{ fontFamily: 'Arial, Helvetica, sans-serif' }}
    >
      <h1 className="text-xl font-semibold">UI template regression fixtures</h1>
      <Button aria-pressed={dark} onClick={() => setDark(!dark)}>
        Dark theme
      </Button>
      <output aria-label="Device class">{mobile ? 'Mobile' : 'Desktop'}</output>
      <Breadcrumb>
        <BreadcrumbList>
          <BreadcrumbItem>
            <BreadcrumbLink href="#overview">Overview</BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbItem>
            <BreadcrumbPage>Templates</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>
      <Label htmlFor="review-name">Review name</Label>
      <Input id="review-name" />
      <Field aria-label="Review field">
        <FieldLabel htmlFor="field-note">Field note</FieldLabel>
        <Input id="field-note" />
      </Field>
      <InputGroup aria-label="Input example">
        <InputGroupAddon htmlFor="group-input">Prefix</InputGroupAddon>
        <InputGroupInput id="group-input" aria-label="Grouped input" />
        <InputGroupAddon align="inline-end" htmlFor="group-input">
          <InputGroupButton onClick={() => setResult('Addon action')}>
            Action
          </InputGroupButton>
        </InputGroupAddon>
      </InputGroup>
      <InputGroup aria-label="Textarea example">
        <InputGroupTextarea id="group-textarea" aria-label="Grouped textarea" />
        <InputGroupAddon align="block-end" htmlFor="group-textarea">
          Notes
        </InputGroupAddon>
      </InputGroup>
      <ButtonGroup aria-label="Review actions">
        <Button onClick={() => setResult('Saved')}>Save example</Button>
        <Button variant="outline" onClick={() => setResult('Reset')}>
          Reset example
        </Button>
      </ButtonGroup>
      <output aria-label="Action result">{result}</output>
      <Pagination>
        <PaginationContent>
          <PaginationItem>
            <PaginationLink href="#page-1" isActive>
              1
            </PaginationLink>
          </PaginationItem>
          <PaginationItem>
            <PaginationLink href="#page-2">2</PaginationLink>
          </PaginationItem>
        </PaginationContent>
      </Pagination>
      <ItemGroup aria-label="Example items">
        <Item>First item</Item>
        <ItemSeparator />
        <Item>Second item</Item>
      </ItemGroup>
      <InputOTP maxLength={6} aria-label="Example digits">
        <InputOTPGroup>
          {[0, 1, 2].map((index) => (
            <InputOTPSlot key={index} index={index} />
          ))}
        </InputOTPGroup>
        <InputOTPSeparator />
        <InputOTPGroup>
          {[3, 4, 5].map((index) => (
            <InputOTPSlot key={index} index={index} />
          ))}
        </InputOTPGroup>
      </InputOTP>
      <Spinner aria-label="Loading example" />
      <Command>
        <CommandInput aria-label="Search examples" />
        <CommandList>
          <CommandItem onSelect={() => setResult('Command chosen')}>
            Example command
          </CommandItem>
        </CommandList>
      </Command>
      <Combobox items={['Bridge', 'Port']}>
        <ComboboxInput aria-label="Example object" showClear />
        <ComboboxContent>
          <ComboboxList>
            {(item) => (
              <ComboboxItem key={item} value={item}>
                {item}
              </ComboboxItem>
            )}
          </ComboboxList>
        </ComboboxContent>
      </Combobox>
      <Button onClick={() => setCount(count === 3 ? 1 : 3)}>
        Toggle slide count
      </Button>
      <Carousel
        className="mx-12 max-w-xs"
        aria-label="Example carousel"
        tabIndex={0}
      >
        <CarouselContent>
          {Array.from({ length: count }, (_, index) => (
            <CarouselItem key={index}>
              <div className="grid h-20 place-items-center rounded border">
                Slide {index + 1}
              </div>
            </CarouselItem>
          ))}
        </CarouselContent>
        <CarouselPrevious />
        <CarouselNext />
      </Carousel>
      <ChartContainer
        config={{ frames: { label: 'Frame count', color: '#2563eb' } }}
        className="h-32"
      >
        <div>
          <ChartTooltipContent
            active
            label="Sample"
            payload={[
              {
                dataKey: () => 24,
                name: 'frames',
                value: 24,
                graphicalItemId: 'example-frames',
              },
            ]}
          />
          <ChartLegendContent
            payload={[{ dataKey: 0, value: 'frames', color: '#2563eb' }]}
            nameKey="frames"
          />
        </div>
      </ChartContainer>
    </main>
  );
}

const root = document.getElementById('ui-test-root');
if (!root) throw new Error('Missing UI review root.');
createRoot(root).render(<TemplateReview />);
