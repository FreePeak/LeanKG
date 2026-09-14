package Warehouse::Inventory;

use strict;
use warnings;
use List::Util qw(sum);

sub new {
    my ($class, %args) = @_;
    return bless { stock => [] }, $class;
}

sub total_value {
    my ($self) = @_;
    my @prices = map { $_->{price} } @{ $self->{stock} };
    return sum @prices;
}

package Warehouse::Report;

sub format_line {
    my ($item) = @_;
    if ($item->{qty} > 0) {
        for my $tag (@{ $item->{tags} }) {
            print "$tag\n";
        }
    }
    return sprintf('%-12s %d', $item->{sku}, $item->{qty});
}

1;
