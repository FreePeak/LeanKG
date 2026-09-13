#!/usr/bin/perl
# Constructs the previous (ganezdragon) Perl grammar could not parse — see
# issue #61. Every one of these must reach the tree tier as a clean parse
# with the ts-parser-perl grammar: subroutine attributes/prototypes, an
# experimental signature, a `method` with a defaulting parameter, a versioned
# package and versioned `use`, postfix dereferencing, a heredoc with
# interpolation, `//` defined-or, and a body-less forward declaration.
package Billing::Invoice 0.35;

use strict;
use warnings;
use List::Util 1.50 qw(sum first);
use experimental 'signatures';

our @EXPORT_OK = qw(render_all);

sub _fmt_amount :prototype($) {
    my ($cents) = @_;
    return sprintf('%.2f', $cents / 100);
}

sub legacy_total {
    my ($self) = @_;
    my @amounts = map { $_->{amount} } $self->{lines}->@*;
    return _fmt_amount(sum @amounts);
}

method render ($self, $indent = 0) {
    my $pad = ' ' x $indent;
    my $head = <<"EOT";
${pad}invoice: @{[ $self->legacy_total ]}
EOT
    my $note = $self->{note} // 'none';
    return "$head$note\n";
}

sub render_all;    # forward declaration: a definition with no body

1;
