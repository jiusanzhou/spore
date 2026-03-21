import numpy as np
import matplotlib.pyplot as plt

class Population:
    def __init__(self, initial_quantity):
        self.quantity = initial_quantity

class Simulation:
    def __init__(self, prey_initial, predator_initial):
        self.prey = Population(prey_initial)
        self.predators = Population(predator_initial)
        self.prey_growth_rate = 0.1
        self.predator_death_rate = 0.1
        self.predation_rate = 0.01

    def step(self):
        prey_growth = self.prey_growth_rate * self.prey.quantity
        predation = self.predation_rate * self.prey.quantity * self.predators.quantity
        predator_deaths = self.predator_death_rate * self.predators.quantity

        self.prey.quantity += prey_growth - predation
        self.predators.quantity += predation - predator_deaths

    def run(self, duration):
        prey_population = []
        predator_population = []
        for _ in range(duration):
            self.step()
            prey_population.append(self.prey.quantity)
            predator_population.append(self.predators.quantity)
        return prey_population, predator_population

# Example run:
simulation = Simulation(prey_initial=40, predator_initial=9)
prey_pop, predator_pop = simulation.run(duration=100)

plt.plot(prey_pop, label="Prey Population")
plt.plot(predator_pop, label="Predator Population")
plt.title("Predator-Prey Dynamics")
plt.xlabel("Time Step")
plt.ylabel("Population Size")
plt.legend()
plt.show()
