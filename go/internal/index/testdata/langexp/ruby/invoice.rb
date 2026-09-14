require 'json'

module App
  class Invoice
    def initialize(total)
      @total = total
    end

    def total_with_tax
      @total * 1.1
    end

    def self.build(attrs)
      new(attrs[:total])
    end
  end
end
